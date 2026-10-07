// Package push delivers Web Push notifications through gina's webpush extension.
//
// gina's sender is asynchronous: a notification is a message to a sender isolate,
// and the outcome comes back as a message to another isolate. The application's
// jobs want a plain call that returns an error, so Service.Send turns one into the
// other: it hands the notification to the sender, parks the calling goroutine on a
// channel, and a tiny "results" isolate, living beside the sender, wakes it with the
// outcome.
//
// Which endpoints are acceptable (HTTPS on port 443 of the known push services) is
// the application's policy and is checked here before anything is sent; gina adds
// its own address check at connect time, so a name that resolves to an internal
// address is refused even if it passes the host list.
package push

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rm4n0s/gina"
	"github.com/rm4n0s/gina/extensions/webpush"

	"github.com/rm4n0s/once-campfire-go-gina/internal/integrations"
)

// typeResults is the isolate type of the results receiver (webpush itself uses 220).
const typeResults gina.TypeID = 2

// ttl and urgency are what the reference sends: keep a message for 28 days, and wake
// a sleeping device for it.
const ttl = 2419200 * time.Second

// Service sends notifications. The zero value is a disabled service: every Send
// reports that Web Push is not configured.
type Service struct {
	// Resolver is used to check, when a subscription is created, that its host is
	// public. (At send time gina checks the address it actually connects to.)
	Resolver integrations.Resolver

	vapid   *webpush.VAPID
	subject string
	relaxed bool // tests: any endpoint, plain HTTP, loopback addresses

	wp      *webpush.WebPush
	sys     atomic.Pointer[gina.System]
	results gina.Handle

	next    atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan webpush.Result
}

// Disabled returns a service that is not configured.
func Disabled() *Service { return &Service{Resolver: net.DefaultResolver} }

// New builds a service from the VAPID key pair as the reference reads it from the
// environment (base64url; the private key is the 32-byte scalar, possibly with
// its leading zeros dropped) and a contact subject (mailto: or https:).
func New(subject, public, private string) (*Service, error) {
	raw, err := decode64(private)
	if err != nil || len(raw) == 0 || len(raw) > 32 {
		return nil, errors.New("invalid VAPID private key")
	}
	scalar := make([]byte, 32)
	copy(scalar[32-len(raw):], raw)
	vapid, err := webpush.ParseVAPID(base64.RawURLEncoding.EncodeToString(scalar))
	if err != nil {
		return nil, err
	}
	want, err := decode64(public)
	if err != nil {
		return nil, errors.New("invalid VAPID public key")
	}
	have, _ := decode64(vapid.PublicKey())
	if !bytes.Equal(want, have) {
		return nil, errors.New("VAPID keys do not match")
	}
	s := Disabled()
	s.vapid, s.subject = vapid, subject
	s.pending = map[uint64]chan webpush.Result{}
	return s, nil
}

// Relax lets the service use plain-HTTP and loopback endpoints that are not push
// services, so tests can aim it at a local fake. Call it before the system is built;
// never in production.
func (s *Service) Relax() { s.relaxed = true }

// Enabled reports whether Web Push is configured.
func (s *Service) Enabled() bool { return s.vapid != nil }

// PublicKey is the application server key handed to browsers (base64url, padded,
// as the reference serves it).
func (s *Service) PublicKey() string {
	raw, _ := decode64(s.vapid.PublicKey())
	return base64.URLEncoding.EncodeToString(raw)
}

// ---- gina wiring (front.Extension) ----

// Install adds the sender and the results receiver to the last shard of spec, a
// service shard (see front.Extension). Send may be called from an HTTP handler,
// which blocks its shard thread, so the isolates that wake it must never share
// that shard.
func (s *Service) Install(spec *gina.SystemSpec) error {
	if !s.Enabled() {
		return nil
	}
	shard := len(spec.Shards) - 1
	wp, err := webpush.New(webpush.Config{
		VAPID: s.vapid, Subject: s.subject, Shard: shard,
		AllowInsecure: s.relaxed, AllowPrivate: s.relaxed,
		Retries: 2, RetryBackoff: 500 * time.Millisecond, Timeout: 15 * time.Second,
	})
	if err != nil {
		return err
	}
	if err := wp.Install(spec); err != nil {
		return err
	}
	spec.Types = append(spec.Types, gina.RegisterType(typeResults,
		gina.TypeOptions{SlotCount: 1, MailboxCapacity: 1024}, nil, s.resultsHandler))
	spec.Shards[shard].Boot = append(spec.Shards[shard].Boot,
		gina.SpawnSpec{Type: typeResults, Group: gina.GroupRoot, Restart: gina.RestartPermanent})
	s.wp, s.results = wp, gina.MakeHandle(uint8(shard), typeResults, 0, 1)
	return nil
}

// Attach starts the workers that talk to the push services.
func (s *Service) Attach(sys *gina.System) error {
	if !s.Enabled() || s.wp == nil {
		return nil
	}
	if err := s.wp.Start(sys); err != nil {
		return err
	}
	s.sys.Store(sys)
	return nil
}

// Close stops the workers.
func (s *Service) Close() {
	if s.wp != nil {
		s.wp.Close()
	}
}

func (s *Service) resultsHandler(_ *struct{}, _ *gina.Ctx, m *gina.Message) gina.Effect {
	switch m.Tag {
	case webpush.TagResult:
		r := *gina.PayloadAs[webpush.Result](m)
		s.mu.Lock()
		ch := s.pending[r.ID]
		delete(s.pending, r.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- r // buffered, one result per id
		}
	case gina.TagShutdown:
		return gina.Done()
	}
	return gina.WaitMessage()
}

// ---- the calls the application makes ----

// Validate checks a new subscription's endpoint: an allowed push service whose
// name resolves to public addresses only.
func (s *Service) Validate(ctx context.Context, endpoint string) error {
	u, err := s.endpoint(endpoint)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = integrations.ResolvePublic(ctx, s.Resolver, u.Hostname())
	return err
}

func (s *Service) endpoint(endpoint string) (*url.URL, error) {
	if s.relaxed {
		return url.Parse(endpoint)
	}
	return integrations.PushEndpoint(endpoint)
}

// Send encrypts message for the subscription and delivers it, waiting for the push
// service's answer. A subscription the push service has dropped yields
// integrations.ErrPushGone, and one whose keys can never work yields
// integrations.ErrPushPoint; the caller deletes both. An endpoint that is not an
// allowed push service is ignored, as in the reference.
func (s *Service) Send(ctx context.Context, endpoint, key, auth string, message []byte) error {
	if !s.Enabled() {
		return errors.New("Web Push is not configured")
	}
	if _, err := s.endpoint(endpoint); err != nil {
		return nil
	}
	sub, err := subscription(endpoint, key, auth)
	if err != nil {
		return integrations.ErrPushPoint
	}
	sys := s.sys.Load()
	if sys == nil {
		return errors.New("Web Push sender is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	id := s.next.Add(1)
	ch := make(chan webpush.Result, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	forget := func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }

	n := &webpush.Notification{ID: id, Sub: sub, Payload: message, TTL: ttl, Urgency: webpush.UrgencyHigh, ReplyTo: s.results}
	if err := n.Validate(); err != nil {
		forget()
		return err
	}
	if r := s.wp.SendExternal(sys, n); r != gina.SendOK {
		forget()
		return fmt.Errorf("push queue refused the notification: %v", r)
	}
	select {
	case r := <-ch:
		switch r.Outcome {
		case webpush.OutcomeDelivered:
			return nil
		case webpush.OutcomeGone:
			return integrations.ErrPushGone
		case webpush.OutcomeInvalid:
			return errors.New("push notification could not be built")
		default:
			return fmt.Errorf("push service returned %d (%s after %d attempts)", r.Status, r.Outcome, r.Attempts)
		}
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

func subscription(endpoint, key, auth string) (webpush.Subscription, error) {
	p256dh, err := decode64(key)
	if err != nil {
		return webpush.Subscription{}, err
	}
	secret, err := decode64(auth)
	if err != nil {
		return webpush.Subscription{}, err
	}
	sub := webpush.Subscription{Endpoint: endpoint, P256dh: p256dh, Auth: secret}
	return sub, sub.Validate()
}

// decode64 reads base64url as browsers and Rails libraries write it: padded or not,
// and tolerating the standard alphabet.
func decode64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.NewReplacer("+", "-", "/", "_").Replace(s), "=")
	return base64.RawURLEncoding.Strict().DecodeString(s)
}
