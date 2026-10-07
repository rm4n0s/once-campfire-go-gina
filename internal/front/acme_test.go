package front_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gtls "github.com/rm4n0s/gina/extensions/tls"

	"github.com/rm4n0s/once-campfire-go-gina/internal/front"
)

// TestACMEWithPebble obtains a real certificate from a local Pebble CA through
// TLS-ALPN-01, answered by the gina TLS server itself, and then serves requests
// with it. It needs the pebble binary (PEBBLE_BIN, or "pebble" on PATH):
//
//	go install github.com/letsencrypt/pebble/v2/cmd/pebble@latest
func TestACMEWithPebble(t *testing.T) {
	bin := os.Getenv("PEBBLE_BIN")
	if bin == "" {
		bin, _ = exec.LookPath("pebble")
	}
	if bin == "" {
		t.Skip("pebble not installed (set PEBBLE_BIN)")
	}
	for _, port := range []int{14000, 5001, 5002} {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			t.Skipf("port %d is busy: %v", port, err)
		} else {
			l.Close()
		}
	}
	dir := t.TempDir()

	certFile, keyFile := pebbleIdentity(t)
	config, _ := json.Marshal(map[string]any{"pebble": map[string]any{
		"listenAddress": "127.0.0.1:14000", "certificate": certFile, "privateKey": keyFile,
		"httpPort": 5002, "tlsPort": 5001, "managementListenAddress": "127.0.0.1:15000",
	}})
	configFile := filepath.Join(dir, "pebble.json")
	os.WriteFile(configFile, config, 0o644)
	t.Setenv("SSL_CERT_FILE", certFile)

	pebble := exec.Command(bin, "-config", configFile)
	pebble.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_VA_ALWAYS_VALID=0")
	output, _ := pebble.StderrPipe()
	if err := pebble.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pebble.Process.Kill(); pebble.Wait() })
	go func() {
		scan := bufio.NewScanner(output)
		for scan.Scan() {
			t.Log("pebble:", scan.Text())
		}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if c, err := net.Dial("tcp", "127.0.0.1:14000"); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pebble did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}

	const domain = "campfire.localhost"
	startACMEServer(t, dir, domain)
	var leaf *x509.Certificate
	limit := time.Now().Add(60 * time.Second)
	for leaf == nil {
		conn, err := tls.Dial("tcp", "127.0.0.1:5001", &tls.Config{InsecureSkipVerify: true, ServerName: domain, NextProtos: []string{"h2", "http/1.1"}})
		if err == nil {
			peer := conn.ConnectionState().PeerCertificates[0]
			conn.Close()
			if peer.Issuer.String() != peer.Subject.String() { // no longer the self-signed placeholder
				leaf = peer
				break
			}
		}
		if time.Now().After(limit) {
			t.Fatalf("no certificate was issued (last error: %v)", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if len(leaf.DNSNames) == 0 || leaf.DNSNames[0] != domain || !strings.Contains(leaf.Issuer.String(), "Pebble") {
		t.Fatalf("unexpected certificate: subject=%v issuer=%v dns=%v", leaf.Subject, leaf.Issuer, leaf.DNSNames)
	}
	// The issued certificate is cached for the next start, and requests work over h2.
	if entries, _ := os.ReadDir(filepath.Join(dir, "acme")); len(entries) == 0 {
		t.Fatal("certificate was not cached")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: domain}, ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:5001")
		}}}
	response, err := client.Get("https://" + domain + "/echo")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || response.ProtoMajor != 2 {
		t.Fatalf("%s %s", response.Proto, response.Status)
	}
}

var (
	pebbleOnce            sync.Once
	pebbleCert, pebbleKey string
	pebbleErr             error
)

// pebbleIdentity returns the certificate and key of Pebble's own HTTPS listener.
// Go reads SSL_CERT_FILE once per process, so repeated runs (-count=N) must trust
// the very same certificate: it is generated once.
func pebbleIdentity(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	pebbleOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pebble-identity-")
		if err != nil {
			pebbleErr = err
			return
		}
		cert, err := gtls.SelfSigned("localhost", "127.0.0.1")
		if err != nil {
			pebbleErr = err
			return
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
		if err != nil {
			pebbleErr = err
			return
		}
		pebbleCert, pebbleKey = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
		for path, block := range map[string]*pem.Block{
			pebbleCert: {Type: "CERTIFICATE", Bytes: cert.Certificate[0]},
			pebbleKey:  {Type: "PRIVATE KEY", Bytes: keyDER},
		} {
			if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
				pebbleErr = err
			}
		}
	})
	if pebbleErr != nil {
		t.Fatal(pebbleErr)
	}
	return pebbleCert, pebbleKey
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func startACMEServer(t *testing.T, dir, domain string) *front.Server {
	t.Helper()
	srv, err := front.Start(front.Config{
		Shards: 2, DisableTarget: true, Gzip: true, TempDir: t.TempDir(), LogRequests: false,
		Domains: []string{domain}, HTTPSPort: 5001, HTTPPort: 0,
		ACMEDirectory: "https://localhost:14000/dir", StoragePath: filepath.Join(dir, "acme"),
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxUpload: 1 << 30,
	}, app(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)
	return srv
}
