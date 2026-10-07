package httpx_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx/httpxtest"
)

func TestHeaderCanonicalisation(t *testing.T) {
	h := httpx.Header{}
	h.Set("x-rev", "1")
	h.Add("X-REV", "2")
	if h.Get("X-Rev") != "1" || len(h.Values("x-rev")) != 2 {
		t.Fatalf("%v", h)
	}
	h.Del("X-REV")
	if h.Get("x-rev") != "" {
		t.Fatal("Del")
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 10, 7, 19, 6, 3, 0, time.UTC)
	for _, s := range []string{"Wed, 07 Oct 2026 19:06:03 GMT", "Wednesday, 07-Oct-26 19:06:03 GMT", "Wed Oct  7 19:06:03 2026"} {
		got, err := httpx.ParseTime(s)
		if err != nil || !got.Equal(want) {
			t.Errorf("%q: %v %v", s, got, err)
		}
	}
	if _, err := httpx.ParseTime("yesterday"); err == nil {
		t.Error("garbage parsed")
	}
}

func TestCookies(t *testing.T) {
	w := httpxtest.NewRecorder()
	httpx.SetCookie(w, &httpx.Cookie{Name: "s", Value: "a b;c\"d", Path: "/", HttpOnly: true, Secure: true, SameSite: httpx.SameSiteLaxMode,
		Expires: time.Date(2046, 10, 7, 19, 6, 3, 0, time.UTC)})
	httpx.SetCookie(w, &httpx.Cookie{Name: "gone", Path: "/", MaxAge: -1})
	got := w.Header().Values("Set-Cookie")
	if len(got) != 2 || got[0] != `s="a bcd"; Path=/; Expires=Sun, 07 Oct 2046 19:06:03 GMT; HttpOnly; Secure; SameSite=Lax` || got[1] != "gone=; Path=/; Max-Age=0" {
		t.Fatalf("%q", got)
	}
	r := httpxtest.NewRequest("GET", "/", nil)
	r.Header.Add("Cookie", `a=1; b="two words"; =bad; c=3`)
	r.Header.Add("Cookie", "d=4")
	var names []string
	for _, c := range r.Cookies() {
		names = append(names, c.Name+"="+c.Value)
	}
	if strings.Join(names, ",") != "a=1,b=two words,c=3,d=4" {
		t.Fatalf("%v", names)
	}
	if _, err := r.Cookie("zzz"); !errors.Is(err, httpx.ErrNoCookie) {
		t.Fatal(err)
	}
	r.AddCookie(&httpx.Cookie{Name: "e", Value: "5"})
	if c, err := r.Cookie("e"); err != nil || c.Value != "5" {
		t.Fatal(c, err)
	}
}

func TestParseForm(t *testing.T) {
	r := httpxtest.NewRequest("POST", "/x?q=1&name=query", strings.NewReader("name=body&a=1&a=2"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if r.PostForm.Get("name") != "body" || r.Form["name"][0] != "body" || r.Form["name"][1] != "query" || len(r.Form["a"]) != 2 || r.FormValue("q") != "1" {
		t.Fatalf("form %v post %v", r.Form, r.PostForm)
	}
	g := httpxtest.NewRequest("GET", "/x?a=1", strings.NewReader("a=2"))
	g.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	g.ParseForm()
	if g.Form.Get("a") != "1" || len(g.PostForm) != 0 {
		t.Fatal("GET bodies are not forms")
	}
}

func TestMaxBytesReader(t *testing.T) {
	r := httpx.MaxBytesReader(nil, io.NopCloser(strings.NewReader("0123456789")), 4)
	data, err := io.ReadAll(r)
	var limit *httpx.MaxBytesError
	if !errors.As(err, &limit) || limit.Limit != 4 || string(data) != "0123" {
		t.Fatalf("%q %v", data, err)
	}
	form := httpxtest.NewRequest("POST", "/", strings.NewReader("a="+strings.Repeat("x", 100)))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Body = httpx.MaxBytesReader(nil, form.Body, 10)
	if err := form.ParseForm(); !errors.As(err, &limit) {
		t.Fatalf("ParseForm past the limit: %v", err)
	}
}

func TestErrorAndRedirect(t *testing.T) {
	w := httpxtest.NewRecorder()
	httpx.Error(w, "nope", 422)
	if w.Code != 422 || w.Body.String() != "nope\n" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("%d %q %v", w.Code, w.Body, w.Header())
	}
	w = httpxtest.NewRecorder()
	httpx.Redirect(w, httpxtest.NewRequest("GET", "/a/b?x=1", nil), "c?y=2", 302)
	if w.Header().Get("Location") != "/a/c?y=2" || !strings.Contains(w.Body.String(), `<a href="/a/c?y=2">Found</a>`) {
		t.Fatalf("%v %q", w.Header(), w.Body)
	}
	w = httpxtest.NewRecorder()
	httpx.Redirect(w, httpxtest.NewRequest("POST", "/", nil), "https://chat.test/é", 303)
	if w.Header().Get("Location") != "https://chat.test/%C3%A9" || w.Body.Len() != 0 {
		t.Fatalf("%v %q", w.Header(), w.Body)
	}
}

func TestServeContent(t *testing.T) {
	content := []byte("0123456789")
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	serve := func(headers map[string]string, method string) *httpxtest.Recorder {
		r := httpxtest.NewRequest(method, "/f.txt", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httpxtest.NewRecorder()
		httpx.ServeContent(w, r, "f.txt", modified, bytes.NewReader(content))
		return w
	}
	for _, c := range []struct {
		name    string
		headers map[string]string
		code    int
		body    string
		rng     string
	}{
		{"whole", nil, 200, "0123456789", ""},
		{"first bytes", map[string]string{"Range": "bytes=0-3"}, 206, "0123", "bytes 0-3/10"},
		{"open ended", map[string]string{"Range": "bytes=7-"}, 206, "789", "bytes 7-9/10"},
		{"suffix", map[string]string{"Range": "bytes=-2"}, 206, "89", "bytes 8-9/10"},
		{"clamped", map[string]string{"Range": "bytes=8-100"}, 206, "89", "bytes 8-9/10"},
		{"unsatisfiable", map[string]string{"Range": "bytes=10-"}, 416, "invalid range: failed to overlap\n", "bytes */10"},
		{"multiple ranges ignored", map[string]string{"Range": "bytes=0-1,3-4"}, 200, "0123456789", ""},
		{"stale if-range", map[string]string{"Range": "bytes=0-1", "If-Range": "Mon, 01 Jan 2001 00:00:00 GMT"}, 200, "0123456789", ""},
		{"fresh if-range", map[string]string{"Range": "bytes=0-1", "If-Range": modified.Format(httpx.TimeFormat)}, 206, "01", "bytes 0-1/10"},
		{"not modified", map[string]string{"If-Modified-Since": modified.Format(httpx.TimeFormat)}, 304, "", ""},
	} {
		w := serve(c.headers, "GET")
		if w.Code != c.code || w.Body.String() != c.body || w.Header().Get("Content-Range") != c.rng {
			t.Errorf("%s: %d %q %q", c.name, w.Code, w.Body, w.Header().Get("Content-Range"))
		}
	}
	w := serve(nil, "GET")
	if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" || w.Header().Get("Last-Modified") != "Fri, 02 Jan 2026 03:04:05 GMT" || w.Header().Get("Content-Length") != "10" {
		t.Errorf("headers %v", w.Header())
	}
}

func TestUnderlyingWalksWrappers(t *testing.T) {
	inner := httpxtest.NewRecorder()
	outer := &wrapper{inner}
	if s, ok := httpx.Underlying[httpx.Streamer](outer); !ok || s != httpx.Streamer(inner) {
		t.Fatal("streamer not found through Unwrap")
	}
	if _, ok := httpx.Underlying[interface{ Nope() }](outer); ok {
		t.Fatal("found a method nobody has")
	}
}

type wrapper struct{ httpx.ResponseWriter }

func (w *wrapper) Unwrap() httpx.ResponseWriter { return w.ResponseWriter }
