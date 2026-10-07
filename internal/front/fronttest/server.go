// Package fronttest runs an httpx.Handler on a real gina server bound to a loopback
// port, for tests that talk to the application the way a browser does.
package fronttest

import (
	"crypto/tls"
	"net/http"
	"strconv"
	"testing"

	gtls "github.com/rm4n0s/gina/extensions/tls"

	"github.com/rm4n0s/once-campfire-go-gina/internal/cable"
	"github.com/rm4n0s/once-campfire-go-gina/internal/front"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
)

// Server is a running test server.
type Server struct {
	URL   string // base URL, without a trailing slash
	Front *front.Server

	client *http.Client
	t      testing.TB
}

// Mode selects the protocol the test server speaks.
type Mode int

const (
	HTTP1 Mode = iota // plain HTTP/1.1
	H2C               // cleartext, HTTP/2 with prior knowledge or HTTP/1.1 (the preface decides)
	TLS               // HTTPS: HTTP/2 or HTTP/1.1 by ALPN, with a self-signed certificate
)

// Options adjusts NewServerWith.
type Options struct {
	Mode   Mode
	Shards int
	Config func(*front.Config)
	// NoHub starts the system without the realtime bus even if a hub is given.
	NoHub bool
	// Extensions run in the same gina system as the server (Web Push, ...).
	Extensions []front.Extension
}

// NewServer starts a plain HTTP/1.1 server (like httptest.NewServer).
func NewServer(t testing.TB, app httpx.Handler, hub *cable.Hub) *Server {
	return NewServerWith(t, app, hub, Options{})
}

// NewServerWith starts a server speaking the requested protocol. It is stopped when
// the test ends.
func NewServerWith(t testing.TB, app httpx.Handler, hub *cable.Hub, o Options) *Server {
	t.Helper()
	cfg := front.Config{
		Shards: max(o.Shards, 1), DisableTarget: true, Gzip: true, TempDir: t.TempDir(),
		ReadTimeout: 30e9, WriteTimeout: 30e9, IdleTimeout: 60e9, MaxUpload: 1 << 30,
		ForwardHeaders: true, GzipCacheBytes: 8 << 20,
	}
	scheme := "http"
	switch o.Mode {
	case H2C:
		cfg.H2C = true
	case TLS:
		cert, err := gtls.SelfSigned("localhost", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Certificates = append(cfg.Certificates, cert)
		scheme = "https"
	}
	if o.Config != nil {
		o.Config(&cfg)
	}
	if o.NoHub {
		hub = nil
	}
	srv, err := front.Start(cfg, app, hub, o.Extensions...)
	if err != nil {
		t.Fatalf("start front server: %v", err)
	}
	port := srv.HTTPPort
	if o.Mode == TLS {
		port = srv.HTTPSPort
	}
	s := &Server{URL: scheme + "://127.0.0.1:" + strconv.Itoa(port), Front: srv, t: t,
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, ForceAttemptHTTP2: true}}}
	t.Cleanup(s.Close)
	return s
}

// Client returns the server's HTTP client: it trusts the test certificate, uses
// HTTP/2 when the server offers it, and keeps no cookies.
func (s *Server) Client() *http.Client { return s.client }

// Close stops the server.
func (s *Server) Close() {
	s.client.CloseIdleConnections()
	s.Front.Stop()
}
