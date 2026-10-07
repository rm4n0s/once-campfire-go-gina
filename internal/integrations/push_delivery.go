package integrations

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrPushGone = errors.New("push subscription is no longer valid")
var ErrPushPoint = errors.New("invalid push subscription P-256 point")
var pushHosts = []string{"jmt17.google.com", "fcm.googleapis.com", "updates.push.services.mozilla.com", "web.push.apple.com", "notify.windows.com"}

// PushEndpoint accepts only HTTPS endpoints on port 443 of the known push services.
func PushEndpoint(value string) (*url.URL, error) {
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return nil, errors.New("invalid endpoint URL")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "" && u.Port() != "443" {
		return nil, errors.New("push endpoint must use HTTPS on port 443")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range pushHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return u, nil
		}
	}
	return nil, errors.New("endpoint is not a permitted push service")
}

// The limits count JSON string contents, matching Rust's notification truncation.
func TruncatePush(text string, limit int) string {
	size := func(r rune) int {
		switch r {
		case '"', '\\', '\n', '\r', '\t', '\b', '\f':
			return 2
		}
		if r < 32 {
			return 6
		}
		return utf8.RuneLen(r)
	}
	total := 0
	for _, r := range text {
		total += size(r)
	}
	if total <= limit {
		return text
	}
	used := len("…")
	for i, r := range text {
		used += size(r)
		if used > limit {
			return text[:i] + "…"
		}
	}
	return text
}
