package httpx

import (
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ServeContent replies with the contents of content, honouring If-None-Match,
// If-Modified-Since, If-Range and a single byte range. A Content-Type already set
// on the response is kept; otherwise it comes from name's extension. When the
// writer is a Streamer the body is sent from content as the peer reads, and
// content is closed afterwards if it is an io.Closer.
func ServeContent(w ResponseWriter, r *Request, name string, modtime time.Time, content io.ReadSeeker) {
	closeContent := func() {
		if c, ok := content.(io.Closer); ok {
			c.Close()
		}
	}
	size, err := content.Seek(0, io.SeekEnd)
	if err == nil {
		_, err = content.Seek(0, io.SeekStart)
	}
	if err != nil {
		closeContent()
		Error(w, "seeker can't seek", 500)
		return
	}
	h := w.Header()
	if h.Get("Content-Type") == "" {
		ctype := mime.TypeByExtension(filepath.Ext(name))
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		h.Set("Content-Type", ctype)
	}
	if !modtime.IsZero() && modtime.Unix() != 0 && h.Get("Last-Modified") == "" {
		h.Set("Last-Modified", modtime.UTC().Format(TimeFormat))
	}
	if notModified(r, h, modtime) {
		closeContent()
		h.Del("Content-Type")
		h.Del("Content-Length")
		w.WriteHeader(304)
		return
	}
	h.Set("Accept-Ranges", "bytes")
	status, offset, length := 200, int64(0), size
	if spec := r.Header.Get("Range"); spec != "" && rangeApplies(r, h, modtime) {
		start, end, ok, satisfiable := parseSingleRange(spec, size)
		switch {
		case ok && !satisfiable:
			closeContent()
			h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			Error(w, "invalid range: failed to overlap", 416)
			return
		case ok:
			status, offset, length = 206, start, end-start+1
			h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		}
	}
	if offset > 0 {
		if _, err := content.Seek(offset, io.SeekStart); err != nil {
			closeContent()
			Error(w, err.Error(), 500)
			return
		}
	}
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(status)
	// HEAD takes the same path: the server computes Content-Length from the body or
	// stream it is given and sends the head only.
	if s, ok := Underlying[Streamer](w); ok {
		var body io.Reader = io.LimitReader(content, length)
		if c, ok := content.(io.Closer); ok {
			body = closingLimit{body, c}
		}
		s.Stream(body, length)
		return
	}
	io.CopyN(w, content, length)
	closeContent()
}

type closingLimit struct {
	io.Reader
	closer io.Closer
}

func (c closingLimit) Close() error { return c.closer.Close() }

func notModified(r *Request, h Header, modtime time.Time) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		etag := h.Get("Etag")
		if etag == "" {
			return false
		}
		if strings.TrimSpace(inm) == "*" {
			return true
		}
		for _, candidate := range strings.Split(inm, ",") {
			if weakEqual(strings.TrimSpace(candidate), etag) {
				return true
			}
		}
		return false
	}
	if ims := r.Header.Get("If-Modified-Since"); ims != "" && !modtime.IsZero() {
		if t, err := ParseTime(ims); err == nil && !modtime.Truncate(time.Second).After(t) {
			return true
		}
	}
	return false
}

func weakEqual(a, b string) bool {
	return strings.TrimPrefix(a, "W/") == strings.TrimPrefix(b, "W/")
}

// rangeApplies evaluates If-Range: the range is honoured when the validator
// still matches.
func rangeApplies(r *Request, h Header, modtime time.Time) bool {
	ir := r.Header.Get("If-Range")
	if ir == "" {
		return true
	}
	if strings.HasPrefix(ir, `"`) {
		return ir == h.Get("Etag")
	}
	t, err := ParseTime(ir)
	return err == nil && !modtime.IsZero() && modtime.Truncate(time.Second).Equal(t)
}

// parseSingleRange handles "bytes=a-b", "bytes=a-" and "bytes=-n". ok is false
// when the header is not a single range this server will act on (it is then
// ignored and the whole body sent).
func parseSingleRange(spec string, size int64) (start, end int64, ok, satisfiable bool) {
	unit, set, found := strings.Cut(spec, "=")
	if !found || strings.TrimSpace(unit) != "bytes" || strings.Contains(set, ",") {
		return 0, 0, false, false
	}
	from, to, found := strings.Cut(strings.TrimSpace(set), "-")
	if !found {
		return 0, 0, false, false
	}
	switch {
	case from == "":
		n, err := strconv.ParseInt(to, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, true, false
		}
		return max(size-n, 0), size - 1, true, true
	default:
		a, err := strconv.ParseInt(from, 10, 64)
		if err != nil || a < 0 {
			return 0, 0, false, false
		}
		b := size - 1
		if to != "" {
			if b, err = strconv.ParseInt(to, 10, 64); err != nil || b < a {
				return 0, 0, false, false
			}
			b = min(b, size-1)
		}
		if a >= size {
			return 0, 0, true, false
		}
		return a, b, true, true
	}
}
