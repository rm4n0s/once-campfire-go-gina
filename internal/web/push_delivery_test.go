package web

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rm4n0s/once-campfire-go-gina/internal/database"
	"github.com/rm4n0s/once-campfire-go-gina/internal/front/fronttest"
)

// decryptPush opens an aes128gcm record (RFC 8291) addressed to receiver.
func decryptPush(t *testing.T, receiver *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	salt, n := body[:16], int(body[20])
	serverPublic := body[21 : 21+n]
	server, err := ecdh.P256().NewPublicKey(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := receiver.ECDH(server)
	info := append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...)
	info = append(info, serverPublic...)
	prk, _ := hkdf.Key(sha256.New, shared, auth, string(info), 32)
	key, _ := hkdf.Key(sha256.New, prk, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, prk, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+n:], nil)
	if err != nil {
		t.Fatal(err)
	}
	return plain[:strings.LastIndexByte(string(plain), 2)]
}

// A message posted in a room reaches the other members' push subscriptions through
// gina's Web Push sender, encrypted for each; a subscription the push service
// reports gone is deleted.
func TestMessagePushesThroughGinaAndDropsGoneSubscriptions(t *testing.T) {
	vapid, _ := ecdh.P256().GenerateKey(rand.Reader)
	t.Setenv("VAPID_PUBLIC_KEY", base64.RawURLEncoding.EncodeToString(vapid.PublicKey().Bytes()))
	t.Setenv("VAPID_PRIVATE_KEY", base64.RawURLEncoding.EncodeToString(vapid.Bytes()))
	t.Setenv("VAPID_SUBJECT", "mailto:ops@example.com")

	// The sender is aimed at loopback fakes, so it is relaxed before the system exists.
	app, _, _, user := testAppWith(t, fronttest.Options{}, func(s *Server) { s.Push.Relax() })
	if !app.Push.Enabled() {
		t.Fatal("Web Push not configured from the environment")
	}
	rooms, err := app.DB.Rooms(context.Background(), user.ID)
	if err != nil || len(rooms) == 0 {
		t.Fatal(rooms, err)
	}
	room := rooms[0].ID
	ctx := context.Background()

	type delivery struct {
		path string
		body []byte
		auth string
	}
	deliveries := make(chan delivery, 8)
	var gone atomic.Bool
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		deliveries <- delivery{r.URL.Path, body, r.Header.Get("Authorization")}
		if gone.Load() {
			w.WriteHeader(410)
			return
		}
		w.WriteHeader(201)
	}))
	defer service.Close()

	// The second member subscribes (a real P-256 key pair stands in for the browser).
	other, err := app.DB.CreateUser(ctx, "Grace", "grace@test", "digest", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.DB.SetInvolvement(ctx, other.ID, room, "everything"); err != nil {
		t.Fatal(err)
	}
	receiver, _ := ecdh.P256().GenerateKey(rand.Reader)
	secret := make([]byte, 16)
	rand.Read(secret)
	endpoint := service.URL + "/push/grace"
	b64 := base64.RawURLEncoding.EncodeToString
	attrs := map[string]*string{"endpoint": &endpoint, "p256dh_key": ptr(b64(receiver.PublicKey().Bytes())), "auth_key": ptr(b64(secret))}
	if err := app.DB.SavePushSubscription(ctx, other.ID, attrs, "test"); err != nil {
		t.Fatal(err)
	}

	message, err := app.DB.CreateMessage(ctx, user.ID, room, "push-1", "<p>hello from the owner</p>", "hello from the owner")
	if err != nil {
		t.Fatal(err)
	}
	app.messageCreated(message, database.Room{ID: room, Name: "All Talk", Type: "Rooms::Open"})

	select {
	case d := <-deliveries:
		if d.path != "/push/grace" || !strings.HasPrefix(d.auth, "vapid t=") {
			t.Fatalf("delivery: %+v", d)
		}
		var payload struct {
			Title   string `json:"title"`
			Options struct{ Body string }
		}
		plain := decryptPush(t, receiver, secret, d.body)
		if err := json.Unmarshal(plain, &payload, json.MatchCaseInsensitiveNames(true)); err != nil || payload.Title != "All Talk" || !strings.Contains(string(plain), "hello from the owner") {
			t.Fatalf("payload %s (%v)", plain, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no push delivery")
	}
	if list, _ := app.DB.PushSubscriptions(ctx, other.ID); len(list) != 1 {
		t.Fatalf("subscription lost after a delivery: %v", list)
	}

	// The push service now says the subscription is gone.
	gone.Store(true)
	app.messageCreated(message, database.Room{ID: room, Name: "All Talk", Type: "Rooms::Open"})
	select {
	case <-deliveries:
	case <-time.After(10 * time.Second):
		t.Fatal("no second delivery")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err := app.DB.PushSubscriptions(ctx, other.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		if len(list) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gone subscription was not deleted")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ptr(s string) *string { return &s }

// The test-notification endpoint waits for the push service from inside a handler.
// That must work with and without the realtime bus: the sender's isolates live on a
// service shard, never on the shard the waiting handler holds.
func TestTestNotificationWaitsForGina(t *testing.T) {
	for _, name := range []string{"with realtime bus", "without realtime bus"} {
		t.Run(name, func(t *testing.T) {
			vapid, _ := ecdh.P256().GenerateKey(rand.Reader)
			t.Setenv("VAPID_PUBLIC_KEY", base64.RawURLEncoding.EncodeToString(vapid.PublicKey().Bytes()))
			t.Setenv("VAPID_PRIVATE_KEY", base64.RawURLEncoding.EncodeToString(vapid.Bytes()))
			var status atomic.Int32
			status.Store(201)
			received := make(chan []byte, 4)
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- body
				w.WriteHeader(int(status.Load()))
			}))
			defer service.Close()
			app, server, cookie, user := testAppWith(t, fronttest.Options{NoHub: name == "without realtime bus"}, func(s *Server) { s.Push.Relax() })
			receiver, _ := ecdh.P256().GenerateKey(rand.Reader)
			secret := make([]byte, 16)
			rand.Read(secret)
			endpoint, b64 := service.URL+"/push/me", base64.RawURLEncoding.EncodeToString
			attrs := map[string]*string{"endpoint": &endpoint, "p256dh_key": ptr(b64(receiver.PublicKey().Bytes())), "auth_key": ptr(b64(secret))}
			if err := app.DB.SavePushSubscription(context.Background(), user.ID, attrs, "test"); err != nil {
				t.Fatal(err)
			}
			list, _ := app.DB.PushSubscriptions(context.Background(), user.ID)
			path := fmt.Sprintf("%s/%d/test_notifications", pushPath, list[0].ID)
			if !strings.HasPrefix(path, "/users/") {
				path = fmt.Sprintf("/users/%d%s", user.ID, path)
			}
			response, data := perform(t, server, "POST", path, "", nil, cookie)
			if response.StatusCode != 302 {
				t.Fatalf("test notification: %s %s", response.Status, data)
			}
			select {
			case body := <-received:
				if plain := decryptPush(t, receiver, secret, body); !strings.Contains(string(plain), "Campfire Test") {
					t.Fatalf("payload %s", plain)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("nothing delivered")
			}
			// A refusal by the push service is reported to the caller rather than swallowed.
			status.Store(403)
			if response, _ = perform(t, server, "POST", path, "", nil, cookie); response.StatusCode != 500 {
				t.Fatalf("rejected push: %s", response.Status)
			}
		})
	}
}
