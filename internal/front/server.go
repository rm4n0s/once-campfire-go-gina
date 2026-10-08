// Package front is the public face of the application: the listeners, TLS,
// certificates and the glue between gina's HTTP/1.1 and HTTP/2 servers and the
// application's httpx.Handler. It replaces the Thruster-like front server built
// on net/http.
//
// Everything runs on one gina system. Shards 0..N-1 each own a SO_REUSEPORT
// listener per port and an isolate per connection (the HTTP handlers run on those
// threads); one extra shard hosts the realtime bus (package cable).
package front

import (
	"context"
	ctls "crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rm4n0s/gina"
	ghttp "github.com/rm4n0s/gina/extensions/http"
	"github.com/rm4n0s/gina/extensions/http2"
	gtls "github.com/rm4n0s/gina/extensions/tls"

	"github.com/rm4n0s/once-campfire-go-gina/internal/cable"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
)

// Isolate type ids. gina's servers use two consecutive ids each.
const (
	typeBus      gina.TypeID = 1
	typeTarget   gina.TypeID = 200
	typePublic   gina.TypeID = 202
	typeRedirect gina.TypeID = 204
	typeHTTPS    gina.TypeID = 210
	typeACME     gina.TypeID = 206
)

// Extension is a component that lives in the same gina system as the HTTP servers
// (an isolate type and perhaps a shard of its own), such as the Web Push sender.
type Extension interface {
	// Install adds the component to the spec, after the HTTP servers and the
	// realtime bus. The last shard of the spec is a service shard, never one that
	// runs HTTP handlers, so a handler may wait on the component without blocking it.
	Install(spec *gina.SystemSpec) error
	// Attach is called once the system exists, before it runs.
	Attach(sys *gina.System) error
	// Close releases what Attach started; called when the server stops, in the
	// reverse order of Install and while the system still runs.
	Close()
}

// Server is a running front server.
type Server struct {
	sys      *gina.System
	stopOnce sync.Once

	extensions []Extension

	// Ports are the ports actually bound (useful when a configured port is 0).
	TargetPort, HTTPPort, HTTPSPort int
}

// Start builds the system, binds the listeners and starts serving. hub may be nil
// when the application has no realtime channel.
//
// Public listeners are dual-stack ("::" accepts IPv4 and IPv6, like Go's ":port");
// on a host without IPv6 they fall back to IPv4 only.
func Start(cfg Config, app httpx.Handler, hub *cable.Hub, extensions ...Extension) (*Server, error) {
	ip := netip.IPv6Unspecified()
	if probe, err := net.Listen("tcp6", "[::1]:0"); err != nil {
		ip = netip.IPv4Unspecified()
	} else {
		probe.Close()
	}
	return start(cfg, app, hub, ip, extensions)
}

func start(cfg Config, app httpx.Handler, hub *cable.Hub, publicIP netip.Addr, extensions []Extension) (*Server, error) {
	if cfg.Shards < 1 {
		cfg.Shards = 1
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if err := os.MkdirAll(cfg.TempDir, 0o755); err != nil {
		return nil, err
	}
	reuse := cfg.Shards > 1
	spec := gina.SystemSpec{Shards: make([]gina.ShardSpec, cfg.Shards)}
	srv := &Server{}
	mailbox := cable.MailboxCapacity
	cache := newGzipCache(cfg.GzipCacheBytes)

	var (
		listenErrs   []func() error
		ports        []func() int
		tlsCfg       *gtls.Config
		acme         *acmeState
		serveAddrFor = func(bind string) (netip.Addr, error) { return netip.ParseAddr(bind) }
	)
	httpTimeouts := func(c *ghttp.Config) {
		c.IdleTimeout, c.ReadTimeout, c.WriteTimeout = cfg.IdleTimeout, cfg.ReadTimeout, cfg.WriteTimeout
		c.MaxBodyBytes = 1 << 20
		c.MaxConns = 4096
		c.ConnMailbox = mailbox
		c.ReusePort = reuse
	}
	addPlain := func(base gina.TypeID, bind string, port int, public bool, out *int) error {
		h := &handlers{app: app, cfg: cfg, cache: cache, public: public}
		c := ghttp.Config{Port: uint16(port), TypeIDBase: base, IP: publicIP}
		httpTimeouts(&c)
		if bind != "" {
			ip, err := serveAddrFor(bind)
			if err != nil {
				return err
			}
			c.IP = ip
		}
		s := ghttp.New(c, h.router())
		if err := s.Install(&spec); err != nil {
			return err
		}
		listenErrs = append(listenErrs, s.ListenErr)
		ports = append(ports, func() int { *out = int(s.Port(0)); return *out })
		return nil
	}

	if cfg.tlsEnabled() {
		var err error
		tlsCfg, acme, err = newTLS(cfg)
		if err != nil {
			return nil, err
		}
	}

	// The internal application listener: no TLS, no forwarded-header injection.
	if !cfg.DisableTarget && cfg.TargetPort != cfg.HTTPPort && (!cfg.tlsEnabled() || cfg.TargetPort != cfg.HTTPSPort) {
		if err := addPlain(typeTarget, cfg.TargetBind, cfg.TargetPort, false, &srv.TargetPort); err != nil {
			return nil, err
		}
	}

	switch {
	case tlsCfg != nil:
		h := &handlers{app: app, cfg: cfg, cache: cache, public: true}
		c := http2.Config{
			Port: uint16(cfg.HTTPSPort), TypeIDBase: typeHTTPS, TLS: tlsCfg, IP: publicIP,
			ReusePort: reuse, ExtendedConnect: true, HTTP1Fallback: true,
			ConnMailbox: mailbox, MaxBodyBytes: 1 << 20, MaxConns: 4096,
			IdleTimeout: cfg.IdleTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		}
		s := http2.New(c, h.router())
		if err := s.Install(&spec); err != nil {
			return nil, err
		}
		listenErrs = append(listenErrs, s.ListenErr)
		ports = append(ports, func() int { srv.HTTPSPort = int(s.Port(0)); return srv.HTTPSPort })

		redirect := ghttp.NewRouter()
		httpsPort := cfg.HTTPSPort
		redirect.NotFound = func(c *ghttp.Context) {
			host := string(c.Req.Header("Host"))
			if name, _, err := net.SplitHostPort(host); err == nil {
				host = name
			}
			if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
				host = "[" + host + "]"
			}
			if p := srv.HTTPSPort; p != 0 && p != 443 {
				httpsPort = p
			}
			if httpsPort != 443 {
				host = net.JoinHostPort(strings.Trim(host, "[]"), fmt.Sprint(httpsPort))
			}
			c.Redirect(301, "https://"+host+string(c.Req.Target))
		}
		rc := ghttp.Config{Port: uint16(cfg.HTTPPort), TypeIDBase: typeRedirect, IP: publicIP}
		httpTimeouts(&rc)
		rs := ghttp.New(rc, redirect)
		if err := rs.Install(&spec); err != nil {
			return nil, err
		}
		listenErrs = append(listenErrs, rs.ListenErr)
		ports = append(ports, func() int { srv.HTTPPort = int(rs.Port(0)); return srv.HTTPPort })
	case cfg.H2C:
		h := &handlers{app: app, cfg: cfg, cache: cache, public: true}
		s := http2.New(http2.Config{
			Port: uint16(cfg.HTTPPort), TypeIDBase: typeHTTPS, IP: publicIP, ReusePort: reuse, ExtendedConnect: true, HTTP1Fallback: true,
			ConnMailbox: mailbox, MaxBodyBytes: 1 << 20, MaxConns: 4096,
			IdleTimeout: cfg.IdleTimeout, ReadTimeout: cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		}, h.router())
		if err := s.Install(&spec); err != nil {
			return nil, err
		}
		listenErrs = append(listenErrs, s.ListenErr)
		ports = append(ports, func() int { srv.HTTPPort = int(s.Port(0)); return srv.HTTPPort })
	default:
		if err := addPlain(typePublic, "", cfg.HTTPPort, true, &srv.HTTPPort); err != nil {
			return nil, err
		}
	}

	if hub != nil {
		hub.Install(&spec, typeBus)
	}
	if hub == nil && len(extensions) > 0 {
		spec.Shards = append(spec.Shards, gina.ShardSpec{}) // a service shard of its own
	}
	for _, e := range extensions {
		if err := e.Install(&spec); err != nil {
			return nil, err
		}
	}
	if acme != nil {
		acme.install(&spec) // a shard of its own: issuing a certificate blocks on the network
	}
	sys, err := gina.NewSystem(spec, gina.Options{})
	if err != nil {
		var details []string
		for _, f := range listenErrs {
			if e := f(); e != nil {
				details = append(details, e.Error())
			}
		}
		if len(details) > 0 {
			return nil, fmt.Errorf("%w (%s)", err, strings.Join(details, "; "))
		}
		return nil, err
	}
	srv.sys = sys
	for _, p := range ports {
		p()
	}
	if hub != nil {
		hub.Attach(sys)
	}
	for _, e := range extensions {
		if err := e.Attach(sys); err != nil {
			for i := len(extensions) - 1; i >= 0; i-- {
				extensions[i].Close()
			}
			sys.Close()
			return nil, err
		}
	}
	srv.extensions = extensions
	sys.Start(gina.RunOptions{Pin: cfg.Pin, ShutdownGrace: 5 * time.Second})
	return srv, nil
}

// Stop shuts the system down gracefully and releases it. Extensions are closed
// first, last installed first, while the system still runs: a job runner drains its
// queues on its shards.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		for i := len(s.extensions) - 1; i >= 0; i-- {
			s.extensions[i].Close()
		}
		s.sys.Stop()
		s.sys.Close()
	})
}

// Serve starts the front server and runs it until ctx is cancelled.
func Serve(ctx context.Context, cfg Config, app httpx.Handler, hub *cable.Hub, extensions ...Extension) error {
	srv, err := Start(cfg, app, hub, extensions...)
	if err != nil {
		return err
	}
	scheme := "http"
	if cfg.tlsEnabled() {
		scheme = "https"
	}
	slog.Info("listening", "public", srv.publicPort(), "scheme", scheme, "target", srv.TargetPort, "shards", cfg.Shards, "domains", cfg.Domains)
	<-ctx.Done()
	srv.Stop()
	return nil
}

func (s *Server) publicPort() int {
	if s.HTTPSPort != 0 {
		return s.HTTPSPort
	}
	return s.HTTPPort
}

// newTLS prepares the TLS configuration of the public listener: a static
// certificate when TLS_CERT_FILE is set, otherwise ACME for TLS_DOMAIN.
func newTLS(cfg Config) (*gtls.Config, *acmeState, error) {
	if len(cfg.Certificates) > 0 {
		return &gtls.Config{Certificates: cfg.Certificates, NextProtos: []string{"h2", "http/1.1"}}, nil, nil
	}
	if cfg.CertFile != "" {
		cert, err := gtls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("TLS_CERT_FILE: %w", err)
		}
		return &gtls.Config{Certificates: []ctls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}, nil, nil
	}
	// Until the first certificate has been issued, handshakes get a throwaway
	// self-signed one so the listener can answer the ACME validation connections.
	placeholder, err := gtls.SelfSigned(cfg.Domains...)
	if err != nil {
		return nil, nil, err
	}
	acme, err := newACME(cfg)
	if err != nil {
		return nil, nil, err
	}
	tlsCfg := &gtls.Config{
		Certificates:   []ctls.Certificate{placeholder},
		NextProtos:     []string{"h2", "http/1.1", gtls.ACMETLS1},
		GetCertificate: acme.getCertificate,
	}
	acme.tls = tlsCfg
	return tlsCfg, acme, nil
}
