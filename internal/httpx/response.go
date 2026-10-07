package httpx

import (
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// ResponseWriter assembles a response. Status and headers are fixed by the first
// call to WriteHeader or Write.
type ResponseWriter interface {
	Header() Header
	Write([]byte) (int, error)
	WriteHeader(status int)
}

// Handler responds to a request.
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

// HandlerFunc adapts a function to a Handler.
type HandlerFunc func(ResponseWriter, *Request)

func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) { f(w, r) }

// Streamer is implemented by response writers that can send a body from a reader
// as the peer's window allows, rather than buffering it. The headers (including
// Content-Type and the status passed to WriteHeader) must be set first. size is
// the exact number of bytes the reader will supply; the writer closes the reader
// if it is an io.Closer.
type Streamer interface {
	Stream(r io.Reader, size int64)
}

// Unwrapper is implemented by response writers that wrap another one.
type Unwrapper interface{ Unwrap() ResponseWriter }

// Underlying walks the Unwrap chain of w looking for a writer that satisfies T.
func Underlying[T any](w ResponseWriter) (T, bool) {
	for w != nil {
		if t, ok := w.(T); ok {
			return t, true
		}
		u, ok := w.(Unwrapper)
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	var zero T
	return zero, false
}

// StatusText returns the reason phrase of an HTTP status code.
func StatusText(code int) string { return statusText[code] }

var statusText = map[int]string{
	100: "Continue", 101: "Switching Protocols",
	200: "OK", 201: "Created", 202: "Accepted", 204: "No Content", 206: "Partial Content",
	301: "Moved Permanently", 302: "Found", 303: "See Other", 304: "Not Modified", 307: "Temporary Redirect", 308: "Permanent Redirect",
	400: "Bad Request", 401: "Unauthorized", 403: "Forbidden", 404: "Not Found", 405: "Method Not Allowed", 406: "Not Acceptable",
	408: "Request Timeout", 409: "Conflict", 410: "Gone", 411: "Length Required", 413: "Content Too Large", 414: "URI Too Long",
	415: "Unsupported Media Type", 416: "Range Not Satisfiable", 422: "Unprocessable Content", 429: "Too Many Requests",
	431: "Request Header Fields Too Large",
	500: "Internal Server Error", 501: "Not Implemented", 502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout",
}

// Common status codes.
const (
	StatusOK          = 200
	StatusFound       = 302
	StatusNotModified = 304
	StatusNotFound    = 404
)

// Error replies with a plain-text error message.
func Error(w ResponseWriter, message string, code int) {
	h := w.Header()
	h.Del("Content-Length")
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	fmt.Fprintln(w, message)
}

// NotFound replies with a 404.
func NotFound(w ResponseWriter, _ *Request) { Error(w, "404 page not found", 404) }

// Redirect replies with a redirect to target, which may be relative to the
// request's path.
func Redirect(w ResponseWriter, r *Request, target string, code int) {
	if u, err := url.Parse(target); err == nil && u.Scheme == "" && u.Host == "" {
		oldpath := r.URL.Path
		if oldpath == "" {
			oldpath = "/"
		}
		if target == "" || target[0] != '/' {
			dir, _ := path.Split(oldpath)
			target = dir + target
		}
		query := ""
		if i := strings.Index(target, "?"); i != -1 {
			target, query = target[:i], target[i:]
		}
		trailing := strings.HasSuffix(target, "/")
		target = path.Clean(target)
		if trailing && !strings.HasSuffix(target, "/") {
			target += "/"
		}
		target += query
	}
	h := w.Header()
	_, hadCT := h["Content-Type"]
	h.Set("Location", hexEscapeNonASCII(target))
	if !hadCT && (r.Method == "GET" || r.Method == "HEAD") {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(code)
	if !hadCT && r.Method == "GET" {
		fmt.Fprintln(w, "<a href=\""+htmlEscape(target)+"\">"+StatusText(code)+"</a>.\n")
	}
}

func hexEscapeNonASCII(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			fmt.Fprintf(&b, "%%%02X", s[i])
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

var htmlReplacer = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")

func htmlEscape(s string) string { return htmlReplacer.Replace(s) }
