package front

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	ghttp "github.com/rm4n0s/gina/extensions/http"

	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"github.com/rm4n0s/once-campfire-go-gina/internal/responsebody"
)

// handlers turns the application's httpx.Handler into routes of a gina router.
// Every method reaches the same catch-all; the application keeps its own routing
// (Rails resolves overlapping routes in declaration order, which gina's
// first-match router could also do, but the application's table is the reference).
type handlers struct {
	app    httpx.Handler
	cfg    Config
	cache  *gzipCache // memoised compressed bodies; shared by every listener
	public bool       // adds X-Forwarded-* and request logging, like Thruster
}

// Router returns a gina router that serves every path and method through h.
func (h *handlers) router() *ghttp.Router {
	r := ghttp.NewRouter()
	r.GET("/*", h.serve)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		// The body of a write goes to disk once it is large, so a big upload costs
		// constant memory; the application sets its own, finer limits.
		r.Handle(method, "/*", h.serveBody).StreamBody().MaxBody(int(min(h.cfg.MaxUpload, 1<<62))).ReadTimeout(h.cfg.ReadTimeout)
	}
	return r
}

func (h *handlers) serve(c *ghttp.Context) {
	req, ok := h.request(c)
	if !ok {
		return
	}
	h.run(c, req)
}

// serveBody collects the request body (in memory when small, in a temporary file
// otherwise) and runs the application when it is complete.
func (h *handlers) serveBody(c *ghttp.Context) {
	req, ok := h.request(c)
	if !ok {
		return
	}
	kind := strings.ToLower(req.Header.Get("Content-Type"))
	limit := h.cfg.MaxUpload
	if strings.HasPrefix(kind, "application/x-www-form-urlencoded") || strings.HasPrefix(kind, "application/json") {
		limit = min(limit, 16<<20)
	}
	if h.cfg.MaxRequestBody > 0 {
		limit = min(limit, h.cfg.MaxRequestBody)
	}
	if req.ContentLength > limit {
		c.StopBody(413, "Request Entity Too Large\n")
		return
	}
	spoolToDisk := req.ContentLength < 0 || req.ContentLength > 1<<20
	var (
		memory bytes.Buffer
		spool  *os.File
		total  int64
	)
	cleanup := func() {
		if spool != nil {
			spool.Close()
			os.Remove(spool.Name())
			spool = nil
		}
	}
	c.OnBody(func(c *ghttp.Context, chunk []byte, last bool) {
		if c.BodyAborted() {
			cleanup()
			return
		}
		total += int64(len(chunk))
		if total > limit {
			cleanup()
			c.StopBody(413, "Request Entity Too Large\n")
			return
		}
		if spoolToDisk && spool == nil && total > 0 {
			f, err := os.CreateTemp(h.cfg.TempDir, "upload-*")
			if err != nil {
				slog.Error("upload spool", "error", err)
				c.StopBody(500, "Internal Server Error\n")
				return
			}
			spool = f
		}
		if len(chunk) > 0 {
			if spool != nil {
				if _, err := spool.Write(chunk); err != nil {
					cleanup()
					slog.Error("upload spool", "error", err)
					c.StopBody(500, "Internal Server Error\n")
					return
				}
			} else {
				memory.Write(chunk)
			}
		}
		if !last {
			return
		}
		defer cleanup()
		req.ContentLength = total
		if spool != nil {
			spool.Seek(0, io.SeekStart)
			req.Body = io.NopCloser(spool)
		} else {
			req.Body = io.NopCloser(bytes.NewReader(memory.Bytes()))
		}
		h.run(c, req)
	})
}

// request converts the gina request into the application's model. A malformed
// target is answered here.
func (h *handlers) request(c *ghttp.Context) (*httpx.Request, bool) {
	g := c.Req
	u, err := url.ParseRequestURI(string(g.Target))
	if err != nil {
		c.String(400, "Bad Request\n")
		return nil, false
	}
	header := make(httpx.Header, len(g.Headers))
	for _, field := range g.Headers {
		header.Add(string(field.Name), string(field.Value))
	}
	method := g.Method
	if method == "" {
		method = "GET"
	}
	req, _ := httpx.NewRequest(method, "/", nil)
	req.URL, req.Header, req.Host = u, header, header.Get("Host")
	req.ContentLength = int64(g.ContentLength)
	if addr, ok := c.RemoteAddr(); ok {
		req.RemoteAddr = addr.String()
	}
	if info, ok := c.TLS(); ok {
		req.TLS = &httpx.TLSState{ServerName: info.ServerName, Protocol: info.ALPN}
		if info.ALPN == "h2" {
			req.Proto = "HTTP/2.0"
		}
	}
	if g.Body != nil {
		req.Body = io.NopCloser(bytes.NewReader(g.Body))
		req.ContentLength = int64(len(g.Body))
	}
	if h.public {
		h.forward(req)
	}
	return req, true
}

// forward mirrors Thruster's header handling: the application sees the client's
// address and the original host and scheme in X-Forwarded-*.
func (h *handlers) forward(r *httpx.Request) {
	if !h.cfg.ForwardHeaders {
		for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Port", "X-Forwarded-Proto", "Forwarded"} {
			r.Header.Del(name)
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = strings.Trim(host[:i], "[]")
	}
	if previous := r.Header.Get("X-Forwarded-For"); previous != "" {
		r.Header.Set("X-Forwarded-For", previous+", "+host)
	} else {
		r.Header.Set("X-Forwarded-For", host)
	}
	if r.Header.Get("X-Forwarded-Host") == "" {
		r.Header.Set("X-Forwarded-Host", r.Host)
	}
	if r.Header.Get("X-Forwarded-Proto") == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		r.Header.Set("X-Forwarded-Proto", scheme)
	}
	r.Header.Set("X-Request-Start", strconv.FormatInt(time.Now().UnixMilli(), 10))
}

func (h *handlers) run(c *ghttp.Context, req *httpx.Request) {
	started := time.Now()
	w := &exchange{c: c, header: httpx.Header{}}
	defer func() {
		if p := recover(); p != nil {
			slog.Error("handler panic", "method", req.Method, "path", req.URL.Path, "panic", p)
			panic(p) // gina answers 500 and drops the connection state
		}
	}()
	h.app.ServeHTTP(w, req)
	w.finish(req, h)
	if h.public && h.cfg.LogRequests {
		slog.Info("request", "method", req.Method, "path", req.URL.Path, "status", w.status, "duration", time.Since(started))
	}
}

// exchange is the httpx.ResponseWriter of one gina exchange. The application
// writes into it; finish copies the result into the gina response.
type exchange struct {
	c         *ghttp.Context
	header    httpx.Header
	status    int
	wrote     bool
	body      bytes.Buffer
	parts     []responsebody.Part // a completed body handed over by WriteBody
	src       io.Reader
	srcSize   int64
	handedOff bool
}

func (e *exchange) Header() httpx.Header { return e.header }

func (e *exchange) WriteHeader(status int) {
	if e.wrote || status < 200 { // informational responses are not supported
		return
	}
	e.wrote, e.status = true, status
}

func (e *exchange) Write(b []byte) (int, error) {
	if !e.wrote {
		e.WriteHeader(200)
	}
	if e.src != nil {
		return 0, io.ErrClosedPipe
	}
	return e.body.Write(b)
}

// WriteBody takes a completed body as parts that carry their own digests, so
// finish keys the compression cache without hashing the bytes again and joins
// them only when it has to.
func (e *exchange) WriteBody(parts []responsebody.Part) (int, error) {
	if !e.wrote {
		e.WriteHeader(200)
	}
	n := 0
	if e.src != nil || e.body.Len() > 0 || e.parts != nil {
		for _, part := range parts {
			k, err := part.WriteTo(e)
			if n += int(k); err != nil {
				return n, err
			}
		}
		return n, nil
	}
	for _, part := range parts {
		n += part.Len()
	}
	e.parts = parts
	return n, nil
}

// Stream sends the body from r without buffering it (httpx.Streamer).
func (e *exchange) Stream(r io.Reader, size int64) {
	if !e.wrote {
		e.WriteHeader(200)
	}
	e.src, e.srcSize = r, size
}

// Exchange exposes the underlying gina exchange to code that switches protocols.
func (e *exchange) Exchange() *ghttp.Context { return e.c }

// Handover records that the response (a WebSocket upgrade, or its rejection) was
// already written to the gina exchange: finish then adds only extra headers.
func (e *exchange) Handover() { e.handedOff = true }

var skipHeaders = map[string]bool{
	"Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Keep-Alive": true, "Date": true,
}

var gzipPools sync.Map // compression level -> *sync.Pool of *gzip.Writer

func gzipWriter(level int) (*gzip.Writer, *sync.Pool) {
	if level == 0 { // unset (level 0 would store, not compress)
		level = gzip.DefaultCompression
	}
	level = min(max(level, gzip.HuffmanOnly), gzip.BestCompression)
	pool, _ := gzipPools.LoadOrStore(level, &sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, level)
		return w
	}})
	return pool.(*sync.Pool).Get().(*gzip.Writer), pool.(*sync.Pool)
}

// compress gzips body, or returns nil when that would not make it smaller. The
// output depends only on the bytes, so it is memoised by their SHA-256: pages that
// several clients receive unchanged (and every static asset) are compressed once.
func (c *gzipCache) compress(body []byte, level int) []byte {
	return c.compressKeyed(sha256.Sum256(body), body, level)
}

// compressParts is compress for a body in parts: the key comes from the parts'
// digests (in a domain of its own) and the bytes are joined only on a miss.
func (c *gzipCache) compressParts(parts []responsebody.Part, level int) []byte {
	digest := responsebody.Digest(parts)
	key := sha256.Sum256(append([]byte("parts\x00"), digest[:]...))
	if packed, ok := c.get(key); ok {
		return packed
	}
	return c.compressKeyed(key, joinParts(parts), level)
}

func joinParts(parts []responsebody.Part) []byte {
	size := 0
	for _, part := range parts {
		size += part.Len()
	}
	buf := bytes.NewBuffer(make([]byte, 0, size))
	for _, part := range parts {
		part.WriteTo(buf)
	}
	return buf.Bytes()
}

func (c *gzipCache) compressKeyed(key [32]byte, body []byte, level int) []byte {
	if packed, ok := c.get(key); ok {
		return packed
	}
	var buf bytes.Buffer
	zw, pool := gzipWriter(level)
	zw.Reset(&buf)
	_, werr := zw.Write(body)
	cerr := zw.Close()
	pool.Put(zw)
	if werr != nil || cerr != nil || buf.Len() >= len(body) {
		return nil
	}
	c.put(key, buf.Bytes())
	return buf.Bytes()
}

func compressible(contentType string) bool {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(contentType, "text/"),
		strings.Contains(contentType, "json"),
		strings.Contains(contentType, "javascript"),
		strings.Contains(contentType, "xml"),
		strings.HasPrefix(contentType, "image/svg"):
		return true
	}
	return false
}

func cleanValue(v string) string {
	if strings.ContainsAny(v, "\r\n") {
		return strings.NewReplacer("\r", " ", "\n", " ").Replace(v)
	}
	return v
}

func (e *exchange) finish(req *httpx.Request, h *handlers) {
	cfg := h.cfg
	c := e.c
	if e.handedOff {
		for name, values := range e.header {
			if skipHeaders[name] || name == "Content-Type" || name == "Upgrade" || strings.HasPrefix(name, "Sec-Websocket") {
				continue
			}
			for _, v := range values {
				c.SetHeader(name, cleanValue(v))
			}
		}
		return
	}
	if !e.wrote {
		e.WriteHeader(200)
	}
	status := e.status
	contentType := e.header.Get("Content-Type")
	body := e.body.Bytes()
	size := len(body)
	for _, part := range e.parts {
		size += part.Len()
	}
	allowed := status >= 200 && status != 204 && status != 304
	if allowed && contentType == "" && (size > 0 || e.src != nil) {
		contentType = "text/html; charset=utf-8"
	}
	encoding := ""
	if allowed && e.src == nil && cfg.Gzip && size >= 1024 && status != 206 &&
		e.header.Get("Content-Encoding") == "" && e.header.Get("Content-Range") == "" &&
		compressible(contentType) && acceptsGzip(req) &&
		!(cfg.DisableGzipOnAuth && (req.Header.Get("Cookie") != "" || e.header.Get("Set-Cookie") != "")) {
		var packed []byte
		if e.parts != nil {
			packed = h.cache.compressParts(e.parts, cfg.CompressionLevel)
		} else {
			packed = h.cache.compress(body, cfg.CompressionLevel)
		}
		if packed != nil {
			body, encoding = packed, "gzip"
		}
	}
	if encoding == "" && e.parts != nil {
		body = joinParts(e.parts)
	}
	for name, values := range e.header {
		if skipHeaders[name] || name == "Content-Type" {
			continue
		}
		if name == "Vary" && encoding != "" {
			continue // merged below
		}
		for _, v := range values {
			c.SetHeader(name, cleanValue(v))
		}
	}
	if encoding != "" {
		c.SetHeader("Content-Encoding", encoding)
		vary := strings.Join(e.header.Values("Vary"), ", ")
		if !hasVaryToken(vary, "Accept-Encoding") {
			if vary != "" {
				vary += ", "
			}
			vary += "Accept-Encoding"
		}
		c.SetHeader("Vary", vary)
	}
	if e.src != nil && allowed {
		c.SendReader(status, contentType, e.srcSize, e.src)
		e.src = nil
		return
	}
	if e.src != nil {
		ghttp.CloseSource(e.src)
		e.src = nil
	}
	if !allowed {
		c.Status(status)
		return
	}
	// A HEAD response carries no body, but gina derives Content-Length from the body
	// it is given. When the handler declared the length of the representation, hand
	// over an unread source of that size so the head says the same as GET would.
	if req.Method == "HEAD" && len(body) == 0 && encoding == "" {
		if n, err := strconv.ParseInt(e.header.Get("Content-Length"), 10, 64); err == nil && n > 0 {
			c.SendReader(status, contentType, n, emptySource{})
			return
		}
	}
	c.Bytes(status, contentType, body)
}

func acceptsGzip(r *httpx.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0") || strings.Contains(params, "q=0.")
		}
	}
	return false
}

func hasVaryToken(vary, token string) bool {
	for _, part := range strings.Split(vary, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) || strings.TrimSpace(part) == "*" {
			return true
		}
	}
	return false
}

// emptySource stands in for the body of a HEAD response; gina never reads it.
type emptySource struct{}

func (emptySource) Read([]byte) (int, error) { return 0, io.EOF }
