package front

import (
	"context"
	ctls "crypto/tls"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	gtls "github.com/rm4n0s/gina/extensions/tls"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// acmeState obtains and renews certificates with Go's autocert (as Thruster does),
// using TLS-ALPN-01 so validation needs only the HTTPS port: gina answers the
// "acme-tls/1" connections itself, from GetCertificate.
//
// gina's TLS server cannot block inside a handshake, so issuance runs in a
// goroutine and the result is installed with SetCertificates; renewal repeats the
// same call (autocert renews a certificate that is within 30 days of expiry).
type acmeState struct {
	cfg     Config
	manager *autocert.Manager
	tls     *gtls.Config
}

func newACME(cfg Config) (*acmeState, error) {
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cfg.StoragePath),
		HostPolicy: autocert.HostWhitelist(cfg.Domains...),
		Client:     &acme.Client{DirectoryURL: cfg.ACMEDirectory},
	}
	if cfg.EABKeyID != "" {
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(cfg.EABKey, "="))
		if err != nil {
			return nil, fmt.Errorf("invalid EAB_HMAC_KEY: %w", err)
		}
		manager.ExternalAccountBinding = &acme.ExternalAccountBinding{KID: cfg.EABKeyID, Key: key}
	}
	return &acmeState{cfg: cfg, manager: manager}, nil
}

// getCertificate answers TLS-ALPN-01 validation connections; everything else
// falls through to the installed certificates.
func (a *acmeState) getCertificate(h *gtls.ClientHelloInfo) (*gtls.Certificate, error) {
	if len(h.SupportedProtos) != 1 || h.SupportedProtos[0] != gtls.ACMETLS1 {
		return nil, nil
	}
	cert, err := a.manager.GetCertificate(&ctls.ClientHelloInfo{ServerName: h.ServerName, SupportedProtos: []string{gtls.ACMETLS1}})
	if err != nil {
		return nil, err
	}
	return gtls.NewCertificate(*cert)
}

// hello is what a modern browser offers: it makes autocert choose an ECDSA key,
// which keeps gina's handshakes cheap.
func hello(domain string) *ctls.ClientHelloInfo {
	return &ctls.ClientHelloInfo{
		ServerName:        domain,
		SupportedProtos:   []string{"h2", "http/1.1"},
		SupportedVersions: []uint16{ctls.VersionTLS13},
		SupportedCurves:   []ctls.CurveID{ctls.X25519, ctls.CurveP256},
		SignatureSchemes:  []ctls.SignatureScheme{ctls.ECDSAWithP256AndSHA256},
		CipherSuites:      []uint16{ctls.TLS_AES_128_GCM_SHA256, ctls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	}
}

func (a *acmeState) issue() error {
	var certs []ctls.Certificate
	for _, domain := range a.cfg.Domains {
		cert, err := a.manager.GetCertificate(hello(domain))
		if err != nil {
			return fmt.Errorf("certificate for %s: %w", domain, err)
		}
		certs = append(certs, *cert)
	}
	return a.tls.SetCertificates(certs...)
}

func (a *acmeState) run(ctx context.Context) {
	every := a.cfg.acmeRenewEvery
	if every == 0 {
		every = 12 * time.Hour
	}
	retry := time.Minute
	for {
		wait := every
		if err := a.issue(); err != nil {
			slog.Error("acme", "error", err, "retry", retry)
			wait = retry
			retry = min(retry*2, time.Hour)
		} else {
			slog.Info("certificates installed", "domains", a.cfg.Domains)
			retry = time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
