// Package httpxtest provides an in-memory ResponseWriter for exercising handlers
// without a server.
package httpxtest

import (
	"bytes"
	"io"

	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
)

// Recorder collects a handler's response.
type Recorder struct {
	Code        int
	HeaderMap   httpx.Header
	Body        *bytes.Buffer
	wroteHeader bool
	snapshot    httpx.Header
}

// NewRecorder returns a Recorder whose status defaults to 200.
func NewRecorder() *Recorder {
	return &Recorder{Code: 200, HeaderMap: httpx.Header{}, Body: new(bytes.Buffer)}
}

func (r *Recorder) Header() httpx.Header { return r.HeaderMap }

func (r *Recorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.Code, r.wroteHeader, r.snapshot = code, true, r.HeaderMap.Clone()
}

func (r *Recorder) Write(b []byte) (int, error) {
	r.WriteHeader(200)
	return r.Body.Write(b)
}

// Stream implements httpx.Streamer by reading the body into memory.
func (r *Recorder) Stream(src io.Reader, size int64) {
	io.Copy(r.Body, src)
	if c, ok := src.(io.Closer); ok {
		c.Close()
	}
}

// Result is the response as the client would see it: the headers as they were
// when WriteHeader was first called.
func (r *Recorder) Result() *Response {
	h := r.snapshot
	if h == nil {
		h = r.HeaderMap.Clone()
	}
	return &Response{StatusCode: r.Code, Header: h, Body: io.NopCloser(bytes.NewReader(r.Body.Bytes()))}
}

// Response is a finished response.
type Response struct {
	StatusCode int
	Header     httpx.Header
	Body       io.ReadCloser
}

// NewRequest is like httptest.NewRequest: it panics on a bad target, and gives the
// request a documentation-range peer address.
func NewRequest(method, target string, body io.Reader) *httpx.Request {
	r, err := httpx.NewRequest(method, target, body)
	if err != nil {
		panic("httpxtest: " + err.Error())
	}
	r.RemoteAddr = "192.0.2.1:1234"
	return r
}
