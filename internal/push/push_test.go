package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/gina"
	"github.com/rm4n0s/gina/extensions/webpush"

	"github.com/rm4n0s/once-campfire-go-gina/internal/integrations"
)

// open decrypts an aes128gcm Web Push body (RFC 8291) for a receiver. It is an
// implementation independent of gina's: it is checked against a ciphertext made by
// the reference first, and then used to check what gina sends.
func open(t *testing.T, receiver *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	if len(body) < 21+65+16 {
		t.Fatalf("short record: %d bytes", len(body))
	}
	salt, n := body[:16], int(body[20])
	serverPublic := body[21 : 21+n]
	server, err := ecdh.P256().NewPublicKey(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := receiver.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	info := append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...)
	info = append(info, serverPublic...)
	prk, _ := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	key, _ := hkdf.Key(sha256.New, prk, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, prk, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+n:], nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	// Strip the padding delimiter (0x02 followed by zeros).
	i := bytes.LastIndexByte(plain, 2)
	if i < 0 || len(bytes.Trim(plain[i+1:], "\x00")) != 0 {
		t.Fatalf("bad padding delimiter in %q", plain)
	}
	return plain[:i]
}

func TestGinaEncryptionInteroperatesWithReference(t *testing.T) {
	raw, err := os.ReadFile("../../reference/crates/campfire/src/integrations/testdata/web_push_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	v := map[string]string{}
	for k, f := range fields {
		var text string
		if json.Unmarshal(f, &text) == nil {
			v[k] = text
		}
	}
	b64 := func(s string) []byte {
		b, err := decode64(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	receiver, err := ecdh.P256().NewPrivateKey(b64(v["receiver_private_key"]))
	if err != nil {
		t.Fatal(err)
	}
	auth := b64(v["auth"])
	// The decryptor reads what the reference produced...
	if got := open(t, receiver, auth, b64(v["ciphertext"])); string(got) != v["message"] {
		t.Fatalf("reference ciphertext decrypts to %q", got)
	}
	// ...so what it reads from gina's encryptor is trustworthy too.
	sub := webpush.Subscription{Endpoint: "https://fcm.googleapis.com/x", P256dh: b64(v["p256dh"]), Auth: auth}
	for _, pad := range []int{0, 2, 100} {
		body, err := webpush.Encrypt(sub, []byte(v["message"]), pad)
		if err != nil {
			t.Fatal(err)
		}
		if got := open(t, receiver, auth, body); string(got) != v["message"] {
			t.Fatalf("pad %d: gina ciphertext decrypts to %q", pad, got)
		}
	}
}

// ---- end to end through a gina system ----

type harness struct {
	svc      *Service
	receiver *ecdh.PrivateKey
	auth     []byte
	key, sec string // the subscription's keys as the database stores them
}

func startService(t *testing.T, relaxed bool) *harness {
	t.Helper()
	vapidKey, _ := ecdh.P256().GenerateKey(rand.Reader)
	svc, err := New("mailto:ops@example.com",
		base64.RawURLEncoding.EncodeToString(vapidKey.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(vapidKey.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	svc.relaxed = relaxed
	spec := gina.SystemSpec{Shards: make([]gina.ShardSpec, 2)}
	if err := svc.Install(&spec); err != nil {
		t.Fatal(err)
	}
	sys, err := gina.NewSystem(spec, gina.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Attach(sys); err != nil {
		t.Fatal(err)
	}
	sys.Start(gina.RunOptions{ShutdownGrace: time.Second})
	t.Cleanup(func() { svc.Close(); sys.Stop(); sys.Close() })

	receiver, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	return &harness{svc, receiver, auth,
		base64.RawURLEncoding.EncodeToString(receiver.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(auth)}
}

func TestSendDeliversAnEncryptedVAPIDSignedMessage(t *testing.T) {
	h := startService(t, true)
	var got struct {
		sync.Mutex
		header http.Header
		body   []byte
	}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Lock()
		defer got.Unlock()
		got.header = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	defer service.Close()

	err := h.svc.Send(context.Background(), service.URL+"/push/abc", h.key, h.sec, []byte(`{"title":"Hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	got.Lock()
	defer got.Unlock()
	if got.header.Get("TTL") != "2419200" || got.header.Get("Urgency") != "high" ||
		got.header.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(got.header.Get("Authorization"), "vapid t=") ||
		!strings.Contains(got.header.Get("Authorization"), ", k=") {
		t.Fatalf("headers: %v", got.header)
	}
	if plain := open(t, h.receiver, h.auth, got.body); string(plain) != `{"title":"Hi"}` {
		t.Fatalf("payload %q", plain)
	}
}

func TestSendMapsPushServiceAnswers(t *testing.T) {
	h := startService(t, true)
	var calls atomic.Int32
	status := atomic.Int32{}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	defer service.Close()
	for _, c := range []struct {
		status int
		check  func(error) bool
		calls  int32
	}{
		{201, func(err error) bool { return err == nil }, 1},
		{404, func(err error) bool { return errors.Is(err, integrations.ErrPushGone) }, 1},
		{410, func(err error) bool { return errors.Is(err, integrations.ErrPushGone) }, 1},
		{400, func(err error) bool { return err != nil && !errors.Is(err, integrations.ErrPushGone) }, 1},
		{403, func(err error) bool { return err != nil && strings.Contains(err.Error(), "403") }, 1},
		{500, func(err error) bool { return err != nil && strings.Contains(err.Error(), "3 attempts") }, 3}, // retried twice
	} {
		calls.Store(0)
		status.Store(int32(c.status))
		err := h.svc.Send(context.Background(), service.URL+"/x", h.key, h.sec, []byte("hello"))
		if !c.check(err) || calls.Load() != c.calls {
			t.Errorf("status %d: err=%v after %d request(s), want %d", c.status, err, calls.Load(), c.calls)
		}
	}
}

func TestSendRefusesWhatCanNeverWork(t *testing.T) {
	h := startService(t, true)
	var calls atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(201) }))
	defer service.Close()
	// Keys that are not a P-256 point / not 16 bytes: the caller deletes the subscription.
	for _, keys := range [][2]string{{"key", "auth"}, {h.key, "AAAA"}, {"", h.sec}, {base64.RawURLEncoding.EncodeToString(make([]byte, 65)), h.sec}} {
		if err := h.svc.Send(context.Background(), service.URL+"/x", keys[0], keys[1], []byte("x")); !errors.Is(err, integrations.ErrPushPoint) {
			t.Errorf("keys %v: %v", keys, err)
		}
	}
	// A payload no push service accepts is an error, but not a reason to delete the subscription.
	err := h.svc.Send(context.Background(), service.URL+"/x", h.key, h.sec, make([]byte, webpush.MaxPayload+1))
	if err == nil || errors.Is(err, integrations.ErrPushPoint) || errors.Is(err, integrations.ErrPushGone) {
		t.Errorf("oversized payload: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("%d requests were made for notifications that cannot be sent", calls.Load())
	}
}

func TestEndpointPolicyAndSSRF(t *testing.T) {
	h := startService(t, false) // production policy
	var calls atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(201) }))
	defer service.Close()
	// Not a known push service (and plain HTTP to loopback): silently ignored, as in the reference.
	for _, endpoint := range []string{service.URL + "/x", "https://127.0.0.1/x", "https://fcm.googleapis.com.attacker.test/x", "http://fcm.googleapis.com/x"} {
		if err := h.svc.Send(context.Background(), endpoint, h.key, h.sec, []byte("x")); err != nil {
			t.Errorf("%s: %v", endpoint, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("a request reached an endpoint outside the allowed push services")
	}
	if err := h.svc.Validate(context.Background(), "https://localhost/x"); err == nil {
		t.Error("localhost accepted as a push service")
	}
}

func TestSendCancelsWithItsContext(t *testing.T) {
	h := startService(t, true)
	release := make(chan struct{})
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release; w.WriteHeader(201) }))
	defer service.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := h.svc.Send(ctx, service.URL+"/x", h.key, h.sec, []byte("x")); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	if len(h.svc.pending) != 0 {
		t.Fatal("abandoned send left its waiter registered")
	}
}

func TestConcurrentSendsGetTheirOwnResults(t *testing.T) {
	h := startService(t, true)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		plain := open(t, h.receiver, h.auth, body)
		// The payload says which status this request must get back.
		var status int
		fmt.Sscanf(string(plain), "status=%d", &status)
		w.WriteHeader(status)
	}))
	defer service.Close()
	var wg sync.WaitGroup
	for i := range 80 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := []int{201, 410, 403}[i%3]
			err := h.svc.Send(context.Background(), service.URL+"/x", h.key, h.sec, fmt.Appendf(nil, "status=%d", status))
			ok := map[int]bool{201: err == nil, 410: errors.Is(err, integrations.ErrPushGone), 403: err != nil && strings.Contains(err.Error(), "403")}[status]
			if !ok {
				t.Errorf("send %d (status %d): %v", i, status, err)
			}
		}()
	}
	wg.Wait()
}

func TestVAPIDKeysFromTheEnvironment(t *testing.T) {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	pub := key.PublicKey().Bytes()
	for name, c := range map[string]struct {
		public, private string
		ok              bool
	}{
		"unpadded":            {base64.RawURLEncoding.EncodeToString(pub), base64.RawURLEncoding.EncodeToString(key.Bytes()), true},
		"padded":              {base64.URLEncoding.EncodeToString(pub), base64.URLEncoding.EncodeToString(key.Bytes()), true},
		"standard alphabet":   {base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(key.Bytes()), true},
		"mismatched pair":     {base64.RawURLEncoding.EncodeToString(pub), base64.RawURLEncoding.EncodeToString(make([]byte, 32)), false},
		"garbage private key": {base64.RawURLEncoding.EncodeToString(pub), "!!!", false},
		"garbage public key":  {"!!!", base64.RawURLEncoding.EncodeToString(key.Bytes()), false},
	} {
		svc, err := New("mailto:a@b.c", c.public, c.private)
		if (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
		if err == nil && svc.PublicKey() != base64.URLEncoding.EncodeToString(pub) {
			t.Errorf("%s: public key served as %q", name, svc.PublicKey())
		}
	}
	// A scalar whose leading zero byte was dropped by whatever printed it still loads.
	for {
		k, _ := ecdh.P256().GenerateKey(rand.Reader)
		if k.Bytes()[0] != 0 {
			continue
		}
		short := base64.RawURLEncoding.EncodeToString(k.Bytes()[1:])
		if _, err := New("mailto:a@b.c", base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), short); err != nil {
			t.Errorf("short scalar: %v", err)
		}
		break
	}
	if Disabled().Enabled() {
		t.Error("zero service is enabled")
	}
	if err := Disabled().Send(context.Background(), "https://fcm.googleapis.com/x", "a", "b", []byte("x")); err == nil {
		t.Error("disabled service sent")
	}
}
