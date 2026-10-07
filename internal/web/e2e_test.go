package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/once-campfire-go-gina/internal/front/fronttest"
	"github.com/rm4n0s/once-campfire-go-gina/internal/rails"
)

// protocols lists the ways a browser can reach the application through gina.
var protocols = []struct {
	name    string
	options fronttest.Options
	h2      bool // the WebSocket rides an HTTP/2 stream
}{
	{"http1", fronttest.Options{Mode: fronttest.HTTP1}, false},
	{"tls-http1", fronttest.Options{Mode: fronttest.TLS}, false},
	{"tls-h2", fronttest.Options{Mode: fronttest.TLS}, true},
}

func cableIdentifier(t *testing.T, fields map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func subscribe(t *testing.T, ws *fronttest.WS, identifier string) {
	t.Helper()
	command, _ := json.Marshal(map[string]string{"command": "subscribe", "identifier": identifier})
	if err := ws.Send(string(command)); err != nil {
		t.Fatal(err)
	}
	ws.Expect(`"confirm_subscription"`, 5*time.Second)
}

func dial(t *testing.T, server *fronttest.Server, cookie *http.Cookie, h2 bool) *fronttest.WS {
	t.Helper()
	header := http.Header{"Cookie": {cookie.Name + "=" + cookie.Value}}
	var ws *fronttest.WS
	var err error
	if h2 {
		ws, err = server.DialWSH2(t, "/cable", header, "actioncable-v1-json")
	} else {
		ws, err = server.DialWS(t, "/cable", header, "actioncable-v1-json")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	if ws.Subprotocol != "actioncable-v1-json" {
		t.Fatalf("subprotocol %q", ws.Subprotocol)
	}
	ws.Expect(`"type":"welcome"`, 5*time.Second)
	return ws
}

func TestActionCableOverGina(t *testing.T) {
	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			app, server, cookie, user := testAppWith(t, p.options)
			ctx := context.Background()
			rooms, err := app.DB.Rooms(ctx, user.ID)
			if err != nil || len(rooms) == 0 {
				t.Fatal(rooms, err)
			}
			room := rooms[0]

			// An unauthenticated socket is refused before the upgrade.
			if _, err := server.DialWS(t, "/cable", nil, "actioncable-v1-json"); err == nil {
				t.Fatal("anonymous WebSocket accepted")
			}

			first := dial(t, server, cookie, p.h2)
			second := dial(t, server, cookie, p.h2)
			messages := cableIdentifier(t, map[string]any{
				"channel":            "RoomMessagesChannel",
				"signed_stream_name": app.Secrets.SignStream(rails.RoomStream(room.Type, room.ID)),
			})
			typing := cableIdentifier(t, map[string]any{"channel": "TypingNotificationsChannel", "room_id": fmt.Sprint(room.ID)})
			subscribe(t, first, messages)
			subscribe(t, second, messages)
			subscribe(t, first, typing)
			subscribe(t, second, typing)

			// A forged stream name is rejected.
			forged, _ := json.Marshal(map[string]string{"command": "subscribe", "identifier": cableIdentifier(t, map[string]any{"channel": "RoomMessagesChannel", "signed_stream_name": "forged"})})
			first.Send(string(forged))
			first.Expect(`"reject_subscription"`, 5*time.Second)

			// Posting a message reaches every subscriber.
			post := server.URL + fmt.Sprintf("/rooms/%d/messages", room.ID)
			body := url.Values{"message[body]": {"<p>over gina</p>"}, "message[client_message_id]": {"cable-1"}}
			request, _ := http.NewRequest("POST", post, strings.NewReader(body.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", server.URL)
			request.Header.Set("Accept", "*/*")
			request.AddCookie(cookie)
			response, err := server.Client().Do(request)
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("post: %v %v", response, err)
			}
			response.Body.Close()
			for name, ws := range map[string]*fronttest.WS{"first": first, "second": second} {
				frame := ws.Expect("over gina", 5*time.Second)
				var envelope struct {
					Identifier string
					Message    string
				}
				if err := json.Unmarshal([]byte(frame), &envelope); err != nil || envelope.Identifier != messages || !strings.Contains(envelope.Message, "<turbo-stream") {
					t.Fatalf("%s: unexpected frame %s (%v)", name, frame, err)
				}
			}

			// Typing notifications travel from one socket to the other.
			data, _ := json.Marshal(map[string]string{"action": "start"})
			command, _ := json.Marshal(map[string]string{"command": "message", "identifier": typing, "data": string(data)})
			first.Send(string(command))
			second.Expect(`"action":"start"`, 5*time.Second)

			// Presence is recorded on subscribe and cleared when the socket closes.
			presence := cableIdentifier(t, map[string]any{"channel": "PresenceChannel", "room_id": fmt.Sprint(room.ID)})
			subscribe(t, first, presence)
			first.Close()

			// Ending the session disconnects the remaining socket.
			request, _ = http.NewRequest("DELETE", server.URL+"/session", nil)
			request.Header.Set("Origin", server.URL)
			request.Header.Set("Accept", "*/*")
			request.AddCookie(cookie)
			if response, err = server.Client().Do(request); err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			second.Expect(`"type":"disconnect"`, 5*time.Second)
			select {
			case <-second.Closed:
			case <-time.After(5 * time.Second):
				t.Fatal("socket stayed open after the session ended")
			}
		})
	}
}

func TestActionCablePingsEvery3Seconds(t *testing.T) {
	_, server, cookie, _ := testApp(t)
	ws := dial(t, server, cookie, false)
	ws.Expect(`"type":"ping"`, 6*time.Second)
}

func TestStreamedUploadAndRangedDownload(t *testing.T) {
	for _, p := range protocols {
		if p.h2 {
			continue // TLS negotiates HTTP/2 by ALPN for Go's client; "tls-http1" covers the rest
		}
		t.Run(p.name, func(t *testing.T) {
			app, server, cookie, user := testAppWith(t, p.options)
			ctx := context.Background()
			rooms, _ := app.DB.Rooms(ctx, user.ID)
			payload := make([]byte, 40<<20)
			rand.Read(payload)
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, _ := form.CreateFormFile("message[attachment]", "big.bin")
			part.Write(payload)
			form.WriteField("message[client_message_id]", "big-upload")
			form.Close()
			request, _ := http.NewRequest("POST", server.URL+fmt.Sprintf("/rooms/%d/messages", rooms[0].ID), &body)
			request.Header.Set("Content-Type", form.FormDataContentType())
			request.Header.Set("Origin", server.URL)
			request.Header.Set("Accept", "*/*")
			request.AddCookie(cookie)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("upload: %s %.200s", response.Status, data)
			}
			if p.options.Mode == fronttest.TLS && response.ProtoMajor != 2 {
				t.Fatalf("expected HTTP/2, got %s", response.Proto)
			}
			messages, err := app.DB.Messages(ctx, rooms[0].ID, 0)
			if err != nil || len(messages) != 1 {
				t.Fatal(messages, err)
			}
			blob, err := app.Storage.Attached(ctx, "Message", messages[0].ID, "attachment")
			if err != nil || blob.ByteSize != int64(len(payload)) {
				t.Fatalf("stored blob: %+v %v", blob, err)
			}
			link := server.URL + app.Storage.BlobURL(blob)

			fetch := func(rangeHeader string) (*http.Response, []byte) {
				request, _ := http.NewRequest("GET", link, nil)
				if rangeHeader != "" {
					request.Header.Set("Range", rangeHeader)
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return response, data
			}
			response, data = fetch("")
			if response.StatusCode != 200 || sha256.Sum256(data) != sha256.Sum256(payload) {
				t.Fatalf("download: %s, %d bytes", response.Status, len(data))
			}
			response, data = fetch("bytes=1000-1999")
			if response.StatusCode != 206 || !bytes.Equal(data, payload[1000:2000]) ||
				response.Header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(payload)) {
				t.Fatalf("range: %s %q", response.Status, response.Header.Get("Content-Range"))
			}
			response, _ = fetch(fmt.Sprintf("bytes=%d-", len(payload)+10))
			if response.StatusCode != 416 {
				t.Fatalf("unsatisfiable range: %s", response.Status)
			}
			head, _ := http.NewRequest("HEAD", link, nil)
			hr, err := server.Client().Do(head)
			if err != nil {
				t.Fatal(err)
			}
			hr.Body.Close()
			if hr.StatusCode != 200 || hr.ContentLength != int64(len(payload)) {
				t.Fatalf("HEAD: %s length %d", hr.Status, hr.ContentLength)
			}
		})
	}
}
