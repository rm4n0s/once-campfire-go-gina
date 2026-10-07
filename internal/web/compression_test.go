package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/rm4n0s/once-campfire-go-gina/internal/front/fronttest"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCompressedOrdinaryPageSurvivesBufferReuse(t *testing.T) {
	handler := httpx.HandlerFunc(func(w httpx.ResponseWriter, r *httpx.Request) {
		buffered := &responseBuffer{ResponseWriter: w}
		fmt.Fprintf(buffered, "<html><body>%s</body></html>", strings.Repeat(r.URL.Query().Get("name"), 300))
		buffered.finish(r)
	})
	server := fronttest.NewServer(t, handler, nil)
	var first []byte
	for i, name := range []string{"Alpha user", "Bravo user", "Alpha user"} {
		request, _ := http.NewRequest("GET", server.URL+"/?name="+strings.ReplaceAll(name, " ", "+"), nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := io.ReadAll(reader)
		reader.Close()
		response.Body.Close()
		want := "<html><body>" + strings.Repeat(name, 300) + "</body></html>"
		if err != nil || response.StatusCode != 200 || string(plain) != want {
			t.Fatal("ordinary page reused stale or incomplete bytes", err)
		}
		digest := sha256.Sum256(plain)
		if response.Header.Get("Etag") != fmt.Sprintf("W/\"%x\"", digest[:16]) {
			t.Fatal("ordinary page validator no longer matches its bytes")
		}
		if i == 0 {
			first = plain
		} else if (i == 2) != bytes.Equal(first, plain) {
			t.Fatal("pooled-buffer reuse changed the completed representation")
		}
	}
}

func TestCompressedRoomTracksEditsAndRechecksAuthorization(t *testing.T) {
	app, _, cookie, user := testApp(t)
	server := fronttest.NewServer(t, app, app.Cable)
	server.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx := context.Background()
	rooms, err := app.DB.Rooms(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	room := rooms[0].ID
	message, err := app.DB.CreateMessage(ctx, user.ID, room, "compressed-message", "before edit", "before edit")
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(encoding string) (int, http.Header, []byte) {
		t.Helper()
		request, _ := http.NewRequest("GET", fmt.Sprintf("%s/rooms/%d", server.URL, room), nil)
		request.AddCookie(cookie)
		request.Header.Set("Accept-Encoding", encoding)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Content-Encoding") == "gzip" {
			reader, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			body, err = io.ReadAll(reader)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode, response.Header, body
	}
	status, headers, first := fetch("gzip")
	_, _, plain := fetch("identity")
	_, _, cached := fetch("gzip")
	if status != 200 || headers.Get("Content-Encoding") != "gzip" || !bytes.Equal(first, plain) || !bytes.Equal(first, cached) {
		t.Fatal("compressed room differs from identity or repeated response")
	}
	if _, err := app.DB.UpdateMessage(ctx, user.ID, message.ID, "after edit", "after edit"); err != nil {
		t.Fatal(err)
	}
	_, changedHeaders, changed := fetch("gzip")
	if !strings.Contains(string(changed), "after edit") || bytes.Equal(first, changed) || headers.Get("ETag") == changedHeaders.Get("ETag") {
		t.Fatal("compressed room did not invalidate after an edit")
	}
	if _, err := app.DB.Write.ExecContext(ctx, "DELETE FROM memberships WHERE user_id=? AND room_id=?", user.ID, room); err != nil {
		t.Fatal(err)
	}
	status, _, denied := fetch("gzip")
	if status != http.StatusFound || strings.Contains(string(denied), "after edit") {
		t.Fatal("compressed-body reuse bypassed revoked membership", status)
	}
}
