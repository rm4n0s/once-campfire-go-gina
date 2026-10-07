package front_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/http2"

	"github.com/rm4n0s/once-campfire-go-gina/internal/front"
	"github.com/rm4n0s/once-campfire-go-gina/internal/front/fronttest"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
)

// probe describes the request the handler saw, as JSON-ish lines.
func app() httpx.Handler {
	mux := map[string]httpx.HandlerFunc{
		"/echo": func(w httpx.ResponseWriter, r *httpx.Request) {
			r.ParseForm()
			cookie, _ := r.Cookie("c")
			value := ""
			if cookie != nil {
				value = cookie.Value
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "method=%s path=%s query=%s form=%s cookie=%s host=%s proto=%s tls=%t remote=%t xff=%s xfp=%s ua=%s",
				r.Method, r.URL.Path, r.URL.RawQuery, r.Form.Get("name"), value, r.Host, r.Proto, r.TLS != nil, r.RemoteAddr != "",
				r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.UserAgent())
		},
		"/sum": func(w httpx.ResponseWriter, r *httpx.Request) {
			h := sha256.New()
			n, err := io.Copy(h, r.Body)
			fmt.Fprintf(w, "%d %x %v %d", n, h.Sum(nil), err, r.ContentLength)
		},
		"/big": func(w httpx.ResponseWriter, r *httpx.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write(bytes.Repeat([]byte("<p>compress me</p>"), 500))
		},
		"/json": func(w httpx.ResponseWriter, r *httpx.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Add("Set-Cookie", "a=1; Path=/")
			w.Header().Add("Set-Cookie", "b=2; Path=/")
			w.Header().Set("X-Injected", "line1\r\nX-Evil: 1")
			w.WriteHeader(201)
			w.Write([]byte(`{"ok":true}`))
		},
		"/panic": func(w httpx.ResponseWriter, r *httpx.Request) { panic("boom") },
		"/redirect": func(w httpx.ResponseWriter, r *httpx.Request) {
			httpx.Redirect(w, r, "/echo", 302)
		},
		"/nocontent": func(w httpx.ResponseWriter, r *httpx.Request) { w.WriteHeader(204) },
		"/stream": func(w httpx.ResponseWriter, r *httpx.Request) {
			payload := bytes.Repeat([]byte("0123456789"), 100_000)
			w.Header().Set("Content-Type", "application/octet-stream")
			httpx.ServeContent(w, r, "stream.bin", testTime, bytes.NewReader(payload))
		},
	}
	return httpx.HandlerFunc(func(w httpx.ResponseWriter, r *httpx.Request) {
		if h, ok := mux[r.URL.Path]; ok {
			h(w, r)
			return
		}
		httpx.NotFound(w, r)
	})
}

func get(t *testing.T, s *fronttest.Server, path string, header map[string]string) (*http.Response, string) {
	t.Helper()
	request, _ := http.NewRequest("GET", s.URL+path, nil)
	for k, v := range header {
		request.Header.Set(k, v)
	}
	response, err := s.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

var modes = []struct {
	name string
	mode fronttest.Mode
}{{"http1", fronttest.HTTP1}, {"tls", fronttest.TLS}}

func TestRequestModel(t *testing.T) {
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: m.mode})
			request, _ := http.NewRequest("POST", s.URL+"/echo?x=1&y=two%20words", strings.NewReader("name=Gina+Go&other=1"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("User-Agent", "probe/1")
			request.AddCookie(&http.Cookie{Name: "c", Value: "chocolate"})
			response, err := s.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			want := "method=POST path=/echo query=x=1&y=two%20words form=Gina Go cookie=chocolate host=127.0.0.1:"
			if !strings.HasPrefix(string(body), want) {
				t.Fatalf("got %q", body)
			}
			for _, part := range []string{"remote=true", "xff=127.0.0.1", "ua=probe/1"} {
				if !strings.Contains(string(body), part) {
					t.Fatalf("missing %q in %q", part, body)
				}
			}
			if m.mode == fronttest.TLS {
				for _, part := range []string{"tls=true", "proto=HTTP/2.0", "xfp=https"} {
					if !strings.Contains(string(body), part) {
						t.Fatalf("missing %q in %q", part, body)
					}
				}
			} else if !strings.Contains(string(body), "xfp=http ") && !strings.Contains(string(body), "xfp=http") {
				t.Fatalf("scheme: %q", body)
			}
		})
	}
}

func TestResponseModel(t *testing.T) {
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: m.mode})
			response, body := get(t, s, "/json", nil)
			if response.StatusCode != 201 || body != `{"ok":true}` || response.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("json: %s %q %v", response.Status, body, response.Header)
			}
			if got := response.Header.Values("Set-Cookie"); len(got) != 2 {
				t.Fatalf("Set-Cookie headers: %v", got)
			}
			if response.Header.Get("X-Evil") != "" {
				t.Fatal("header injection through a response header value")
			}
			response, _ = get(t, s, "/nocontent", nil)
			if response.StatusCode != 204 {
				t.Fatal(response.Status)
			}
			client := s.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			response, _ = get(t, s, "/redirect", nil)
			if response.StatusCode != 302 || response.Header.Get("Location") != "/echo" {
				t.Fatalf("redirect: %s %v", response.Status, response.Header)
			}
			response, _ = get(t, s, "/missing", nil)
			if response.StatusCode != 404 {
				t.Fatal(response.Status)
			}
			// A panicking handler is a 500; the server keeps serving.
			response, _ = get(t, s, "/panic", nil)
			if response.StatusCode != 500 {
				t.Fatalf("panic: %s", response.Status)
			}
			if response, body = get(t, s, "/echo", nil); response.StatusCode != 200 || !strings.HasPrefix(body, "method=GET") {
				t.Fatalf("after panic: %s %q", response.Status, body)
			}
		})
	}
}

func TestGzip(t *testing.T) {
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: m.mode})
			plain := strings.Repeat("<p>compress me</p>", 500)
			response, body := get(t, s, "/big", map[string]string{"Accept-Encoding": "gzip"})
			if response.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(response.Header.Get("Vary"), "Accept-Encoding") {
				t.Fatalf("not compressed: %v", response.Header)
			}
			reader, err := gzip.NewReader(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(reader)
			if string(got) != plain {
				t.Fatal("gzip body differs")
			}
			response, body = get(t, s, "/big", map[string]string{"Accept-Encoding": "identity"})
			if response.Header.Get("Content-Encoding") != "" || body != plain {
				t.Fatal("identity request was compressed")
			}
			response, body = get(t, s, "/big", map[string]string{"Accept-Encoding": "gzip;q=0"})
			if response.Header.Get("Content-Encoding") != "" || body != plain {
				t.Fatal("gzip;q=0 was compressed")
			}
		})
	}
}

func TestUploadsOfEverySize(t *testing.T) {
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: m.mode, Config: func(c *front.Config) { c.MaxRequestBody = 64 << 20 }})
			for _, size := range []int{0, 1, 4095, 1 << 20, 1<<20 + 1, 7<<20 + 123, 33 << 20} {
				payload := make([]byte, size)
				rand.Read(payload)
				request, _ := http.NewRequest("PUT", s.URL+"/sum", bytes.NewReader(payload))
				response, err := s.Client().Do(request)
				if err != nil {
					t.Fatalf("size %d: %v", size, err)
				}
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				want := fmt.Sprintf("%d %x <nil> %d", size, sha256.Sum256(payload), size)
				if string(body) != want {
					t.Fatalf("size %d: %q want %q", size, body, want)
				}
			}
			// Unknown length (chunked / h2 without content-length) goes through the disk spool.
			pr, pw := io.Pipe()
			payload := make([]byte, 3<<20)
			rand.Read(payload)
			go func() { pw.Write(payload); pw.Close() }()
			request, _ := http.NewRequest("POST", s.URL+"/sum", pr)
			response, err := s.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if !strings.HasPrefix(string(body), fmt.Sprintf("%d %x", len(payload), sha256.Sum256(payload))) {
				t.Fatalf("chunked upload: %q", body)
			}
			// Over the configured limit.
			request, _ = http.NewRequest("POST", s.URL+"/sum", bytes.NewReader(make([]byte, 65<<20)))
			response, err = s.Client().Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode != 413 {
					t.Fatalf("oversized upload: %s", response.Status)
				}
			}
		})
	}
}

func TestRangeAndHead(t *testing.T) {
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: m.mode})
			payload := strings.Repeat("0123456789", 100_000)
			response, body := get(t, s, "/stream", nil)
			if response.StatusCode != 200 || body != payload || response.Header.Get("Accept-Ranges") != "bytes" {
				t.Fatalf("full: %s len=%d", response.Status, len(body))
			}
			response, body = get(t, s, "/stream", map[string]string{"Range": "bytes=10-19"})
			if response.StatusCode != 206 || body != "0123456789" || response.Header.Get("Content-Range") != "bytes 10-19/1000000" {
				t.Fatalf("range: %s %q %q", response.Status, body, response.Header.Get("Content-Range"))
			}
			response, body = get(t, s, "/stream", map[string]string{"Range": "bytes=-5"})
			if response.StatusCode != 206 || body != "56789" {
				t.Fatalf("suffix range: %s %q", response.Status, body)
			}
			response, _ = get(t, s, "/stream", map[string]string{"Range": "bytes=5000000-"})
			if response.StatusCode != 416 {
				t.Fatalf("beyond: %s", response.Status)
			}
			request, _ := http.NewRequest("HEAD", s.URL+"/stream", nil)
			head, err := s.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			head.Body.Close()
			if head.StatusCode != 200 || head.ContentLength != 1_000_000 {
				t.Fatalf("head: %s %d", head.Status, head.ContentLength)
			}
		})
	}
}

func TestConcurrentClients(t *testing.T) {
	s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: fronttest.TLS, Shards: 3})
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 30 {
				request, _ := http.NewRequest("GET", s.URL+"/echo?n="+fmt.Sprint(i*100+j), nil)
				response, err := s.Client().Do(request)
				if err != nil {
					errs <- err
					return
				}
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if !strings.Contains(string(body), fmt.Sprintf("query=n=%d ", i*100+j)) {
					errs <- fmt.Errorf("crossed responses: %q", body)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestHTTPRedirectsToHTTPSAndHTTP1Fallback(t *testing.T) {
	s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: fronttest.TLS})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/echo?a=b", s.Front.HTTPPort))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	u, _ := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != 301 || u.Scheme != "https" || u.Path != "/echo" || u.RawQuery != "a=b" || u.Port() != fmt.Sprint(s.Front.HTTPSPort) {
		t.Fatalf("redirect: %s %q", response.Status, response.Header.Get("Location"))
	}
	// The same TLS port serves HTTP/1.1 to clients that do not offer h2.
	one := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}}, ForceAttemptHTTP2: false}}
	response, err = one.Get(s.URL + "/echo")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.ProtoMajor != 1 || response.StatusCode != 200 {
		t.Fatalf("fallback: %s %s", response.Proto, response.Status)
	}
	// Garbage is closed, not served.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", s.Front.HTTPSPort))
	if err != nil {
		t.Fatal(err)
	}
	conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if n, _ := conn.Read(make([]byte, 16)); n > 0 {
		t.Fatal("plaintext request answered on the TLS port")
	}
	conn.Close()
}

func TestH2C(t *testing.T) {
	s := fronttest.NewServerWith(t, app(), nil, fronttest.Options{Mode: fronttest.H2C})
	// Go's client speaks HTTP/1.1 here: the first bytes decide, and no preface means HTTP/1.1.
	response, body := get(t, s, "/echo", nil)
	if response.StatusCode != 200 || response.ProtoMajor != 1 || !strings.HasPrefix(body, "method=GET") {
		t.Fatalf("%s %s", response.Proto, response.Status)
	}
	// A prior-knowledge HTTP/2 client on the same port.
	h2 := &http.Client{Transport: &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}}
	request, _ := http.NewRequest("POST", s.URL+"/echo", strings.NewReader("name=h2c"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h2.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.ProtoMajor != 2 || !strings.Contains(string(data), "form=h2c") {
		t.Fatalf("h2c: %s %q", response.Proto, data)
	}
}

func TestBadRequestTarget(t *testing.T) {
	s := fronttest.NewServer(t, app(), nil)
	conn, err := net.Dial("tcp", strings.TrimPrefix(s.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET /%zz%\x7f HTTP/1.1\r\nHost: x\r\n\r\n"))
	reply := make([]byte, 64)
	n, _ := conn.Read(reply)
	if !strings.HasPrefix(string(reply[:n]), "HTTP/1.1 400") && n != 0 {
		t.Fatalf("got %q", reply[:n])
	}
}
