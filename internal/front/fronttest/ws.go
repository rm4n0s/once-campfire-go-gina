package fronttest

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

// WS is a minimal WebSocket client for tests: it speaks RFC 6455 over an HTTP/1.1
// upgrade or over an HTTP/2 extended CONNECT stream (RFC 8441).
type WS struct {
	// Status is the status of the handshake response (101 over HTTP/1.1, 200 over
	// HTTP/2); Header is its header.
	Status      int
	Header      http.Header
	Subprotocol string
	// Closed receives the close code once the server closed the connection (or 1006
	// if the transport ended without a close frame).
	Closed chan uint16

	t        testing.TB
	w        io.Writer
	closer   func()
	messages chan string
	wmu      sync.Mutex
}

// DialWS opens a WebSocket to path ("/cable") over HTTP/1.1.
func (s *Server) DialWS(t testing.TB, path string, header http.Header, subprotocols ...string) (*WS, error) {
	t.Helper()
	u, _ := url.Parse(s.URL)
	var conn net.Conn
	var err error
	if u.Scheme == "https" {
		conn, err = tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	} else {
		conn, err = net.Dial("tcp", u.Host)
	}
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	key := base64.StdEncoding.EncodeToString(nonce[:])
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, u.Host, key)
	origin := "http://" + u.Host
	if u.Scheme == "https" {
		origin = "https://" + u.Host
	}
	fmt.Fprintf(&b, "Origin: %s\r\n", origin)
	if len(subprotocols) > 0 {
		fmt.Fprintf(&b, "Sec-WebSocket-Protocol: %s\r\n", strings.Join(subprotocols, ", "))
	}
	for name, values := range header {
		for _, v := range values {
			fmt.Fprintf(&b, "%s: %s\r\n", name, v)
		}
	}
	b.WriteString("\r\n")
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err = io.WriteString(conn, b.String()); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	ws := &WS{Status: resp.StatusCode, Header: resp.Header, t: t, w: conn, closer: func() { conn.Close() }}
	if resp.StatusCode != 101 {
		conn.Close()
		return ws, fmt.Errorf("handshake refused: %s", resp.Status)
	}
	ws.Subprotocol = resp.Header.Get("Sec-WebSocket-Protocol")
	ws.start(br)
	return ws, nil
}

// DialWSH2 opens a WebSocket over an HTTP/2 stream. The server must be in TLS mode.
func (s *Server) DialWSH2(t testing.TB, path string, header http.Header, subprotocols ...string) (*WS, error) {
	t.Helper()
	u, _ := url.Parse(s.URL)
	transport := &http2.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}}
	cc, err := transport.NewClientConn(mustDialTLS(u.Host))
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("CONNECT", s.URL+path, pr)
	req.Header = http.Header{}
	req.Header.Set(":protocol", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Origin", s.URL)
	if len(subprotocols) > 0 {
		req.Header.Set("Sec-WebSocket-Protocol", strings.Join(subprotocols, ", "))
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	req.ContentLength = -1
	resp, err := cc.RoundTrip(req)
	if err != nil {
		pw.Close()
		cc.Close()
		return nil, err
	}
	ws := &WS{Status: resp.StatusCode, Header: resp.Header, t: t, w: pw, closer: func() { pw.Close(); resp.Body.Close(); cc.Close() }}
	if resp.StatusCode != 200 {
		ws.closer()
		return ws, fmt.Errorf("handshake refused: %s", resp.Status)
	}
	ws.Subprotocol = resp.Header.Get("Sec-WebSocket-Protocol")
	ws.start(bufio.NewReader(resp.Body))
	return ws, nil
}

func mustDialTLS(addr string) net.Conn {
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err != nil {
		panic(err)
	}
	return c
}

func (c *WS) start(r *bufio.Reader) {
	c.messages = make(chan string, 1024)
	c.Closed = make(chan uint16, 1)
	go func() {
		defer close(c.messages)
		for {
			var head [2]byte
			if _, err := io.ReadFull(r, head[:]); err != nil {
				c.Closed <- 1006
				return
			}
			n := int(head[1] & 0x7f)
			switch n {
			case 126:
				var l [2]byte
				io.ReadFull(r, l[:])
				n = int(binary.BigEndian.Uint16(l[:]))
			case 127:
				var l [8]byte
				io.ReadFull(r, l[:])
				n = int(binary.BigEndian.Uint64(l[:]))
			}
			payload := make([]byte, n)
			if _, err := io.ReadFull(r, payload); err != nil {
				c.Closed <- 1006
				return
			}
			switch op := head[0] & 0x0f; op {
			case 1, 2:
				c.messages <- string(payload)
			case 8:
				code := uint16(1005)
				if len(payload) >= 2 {
					code = binary.BigEndian.Uint16(payload)
				}
				c.writeFrame(8, payload)
				c.Closed <- code
				return
			case 9:
				c.writeFrame(10, payload)
			}
		}
	}()
}

func (c *WS) writeFrame(op byte, payload []byte) error {
	var mask [4]byte
	rand.Read(mask[:])
	frame := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n < 1<<16:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.w.Write(frame)
	return err
}

// Send writes a text message.
func (c *WS) Send(text string) error { return c.writeFrame(1, []byte(text)) }

// Next returns the next text message, or fails the test after timeout.
func (c *WS) Next(timeout time.Duration) (string, bool) {
	select {
	case m, ok := <-c.messages:
		return m, ok
	case <-time.After(timeout):
		return "", false
	}
}

// Expect reads messages until one contains substr, or fails the test.
func (c *WS) Expect(substr string, timeout time.Duration) string {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m, ok := c.Next(time.Until(deadline))
		if !ok {
			c.t.Fatalf("no message containing %q within %v", substr, timeout)
		}
		if strings.Contains(m, substr) {
			return m
		}
	}
}

// Close drops the connection.
func (c *WS) Close() { c.closer() }
