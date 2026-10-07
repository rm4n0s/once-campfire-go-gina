package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strings"
)

// TLSState is present on a Request that arrived over TLS.
type TLSState struct {
	ServerName string
	Protocol   string // negotiated ALPN protocol: "h2" or "http/1.1"
}

// Request is a received HTTP request.
type Request struct {
	Method        string
	URL           *url.URL
	Proto         string // "HTTP/1.1" or "HTTP/2.0"
	Header        Header
	Body          io.ReadCloser
	ContentLength int64 // -1 when unknown
	Host          string
	RemoteAddr    string // the peer's host:port
	TLS           *TLSState

	Form, PostForm url.Values
	MultipartForm  *multipart.Form

	ctx        context.Context
	pathValues map[string]string
}

// NewRequest builds a request for tests and internal sub-requests. target is the
// request target ("/rooms/1?x=2"); body may be nil.
func NewRequest(method, target string, body io.Reader) (*Request, error) {
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return nil, err
	}
	host := "example.com"
	if u.Host != "" {
		host = u.Host
	}
	r := &Request{Method: method, URL: u, Proto: "HTTP/1.1", Header: Header{}, Host: host, ContentLength: 0, ctx: context.Background()}
	switch b := body.(type) {
	case nil:
		r.Body = io.NopCloser(bytes.NewReader(nil))
	case *bytes.Reader:
		r.ContentLength = int64(b.Len())
		r.Body = io.NopCloser(b)
	case *strings.Reader:
		r.ContentLength = int64(b.Len())
		r.Body = io.NopCloser(b)
	case *bytes.Buffer:
		r.ContentLength = int64(b.Len())
		r.Body = io.NopCloser(b)
	default:
		r.ContentLength = -1
		r.Body = io.NopCloser(body)
	}
	return r, nil
}

// Context returns the request's context (never nil).
func (r *Request) Context() context.Context {
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

// WithContext returns a shallow copy of r with its context replaced.
func (r *Request) WithContext(ctx context.Context) *Request {
	if ctx == nil {
		panic("nil context")
	}
	r2 := new(Request)
	*r2 = *r
	r2.ctx = ctx
	return r2
}

// PathValue returns the value of a route wildcard.
func (r *Request) PathValue(name string) string { return r.pathValues[name] }

// SetPathValue records a route wildcard value.
func (r *Request) SetPathValue(name, value string) {
	if r.pathValues == nil {
		r.pathValues = map[string]string{}
	}
	r.pathValues[name] = value
}

// UserAgent returns the User-Agent header.
func (r *Request) UserAgent() string { return r.Header.Get("User-Agent") }

// Cookies returns every cookie sent with the request.
func (r *Request) Cookies() []*Cookie { return ParseCookies(r.Header.Values("Cookie")) }

// ErrNoCookie is returned by Cookie when the request has none of that name.
var ErrNoCookie = errors.New("http: named cookie not present")

// Cookie returns the first cookie with the given name.
func (r *Request) Cookie(name string) (*Cookie, error) {
	for _, c := range r.Cookies() {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, ErrNoCookie
}

// AddCookie appends a cookie to the request's Cookie header.
func (r *Request) AddCookie(c *Cookie) {
	pair := c.Name + "=" + sanitizeValue(c.Value)
	if existing := r.Header.Get("Cookie"); existing != "" {
		r.Header.Set("Cookie", existing+"; "+pair)
	} else {
		r.Header.Set("Cookie", pair)
	}
}

// FormValue returns the first value of a form or query field.
func (r *Request) FormValue(key string) string {
	if r.Form == nil {
		r.ParseForm()
	}
	return r.Form.Get(key)
}

// ErrMissingFile means a multipart field has no file.
var ErrMissingFile = errors.New("http: no such file")

// FormFile returns the first file of a multipart field that was parsed into
// memory or onto disk by the server.
func (r *Request) FormFile(key string) (multipart.File, *multipart.FileHeader, error) {
	if r.MultipartForm == nil {
		return nil, nil, ErrMissingFile
	}
	if files := r.MultipartForm.File[key]; len(files) > 0 {
		f, err := files[0].Open()
		return f, files[0], err
	}
	return nil, nil, ErrMissingFile
}

// MaxBytesError is returned by a reader made with MaxBytesReader when its limit
// is exceeded.
type MaxBytesError struct{ Limit int64 }

func (e *MaxBytesError) Error() string { return "http: request body too large" }

type maxBytesReader struct {
	r    io.ReadCloser
	n    int64
	err  error
	left int64
}

// MaxBytesReader limits how much of r can be read; reading past n fails with a
// *MaxBytesError.
func MaxBytesReader(_ ResponseWriter, r io.ReadCloser, n int64) io.ReadCloser {
	if n < 0 {
		n = 0
	}
	return &maxBytesReader{r: r, n: n, left: n}
}

func (l *maxBytesReader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p))-1 > l.left {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	if int64(n) <= l.left {
		l.left -= int64(n)
		l.err = err
		return n, err
	}
	n = int(l.left)
	l.left = 0
	l.err = &MaxBytesError{l.n}
	return n, l.err
}

func (l *maxBytesReader) Close() error { return l.r.Close() }

// ParseForm populates Form and PostForm from the query string and, for POST, PUT
// and PATCH requests with a urlencoded body, from the body (at most 10 MiB unless
// the body is already limited with MaxBytesReader).
func (r *Request) ParseForm() error {
	var err error
	if r.PostForm == nil {
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
			r.PostForm, err = parsePostForm(r)
		}
		if r.PostForm == nil {
			r.PostForm = make(url.Values)
		}
	}
	if r.Form == nil {
		if len(r.PostForm) > 0 {
			r.Form = make(url.Values)
			for k, v := range r.PostForm {
				r.Form[k] = append(r.Form[k], v...)
			}
		}
		var query url.Values
		if r.URL != nil {
			var e error
			query, e = url.ParseQuery(r.URL.RawQuery)
			if err == nil {
				err = e
			}
		}
		if r.Form == nil {
			r.Form = make(url.Values)
		}
		for k, v := range query {
			r.Form[k] = append(r.Form[k], v...)
		}
	}
	return err
}

func parsePostForm(r *Request) (url.Values, error) {
	if r.Body == nil {
		return nil, errors.New("missing form body")
	}
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.Header.Get("Content-Type") != "" {
		return nil, err
	}
	if kind != "application/x-www-form-urlencoded" {
		return nil, nil
	}
	var reader io.Reader = r.Body
	if _, limited := r.Body.(*maxBytesReader); !limited {
		reader = io.LimitReader(r.Body, 10<<20+1)
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if _, limited := r.Body.(*maxBytesReader); !limited && len(b) > 10<<20 {
		return nil, errors.New("http: POST too large")
	}
	return url.ParseQuery(string(b))
}
