package httpx

import (
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// Header maps canonical header names to their values.
type Header map[string][]string

// CanonicalKey returns the canonical form of a header name ("x-rev" -> "X-Rev").
func CanonicalKey(name string) string { return textproto.CanonicalMIMEHeaderKey(name) }

func (h Header) Add(key, value string) { k := CanonicalKey(key); h[k] = append(h[k], value) }
func (h Header) Set(key, value string) { h[CanonicalKey(key)] = []string{value} }
func (h Header) Del(key string)        { delete(h, CanonicalKey(key)) }

// Get returns the first value of a header, or "".
func (h Header) Get(key string) string {
	if v := h[CanonicalKey(key)]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Values returns every value of a header.
func (h Header) Values(key string) []string { return h[CanonicalKey(key)] }

// Clone returns a deep copy.
func (h Header) Clone() Header {
	out := make(Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Keys returns the header names in sorted order.
func (h Header) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TimeFormat is the HTTP date format (RFC 9110 §5.6.7).
const TimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// ParseTime parses an HTTP date in any of the three formats servers must accept.
func ParseTime(text string) (time.Time, error) {
	var err error
	for _, layout := range []string{TimeFormat, time.RFC850, time.ANSIC} {
		var t time.Time
		if t, err = time.Parse(layout, text); err == nil {
			return t, nil
		}
	}
	return time.Time{}, err
}

func hasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
