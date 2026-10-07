package httpx

import (
	"strconv"
	"strings"
	"time"
)

type SameSite int

const (
	SameSiteDefaultMode SameSite = iota + 1
	SameSiteLaxMode
	SameSiteStrictMode
	SameSiteNoneMode
)

// Cookie is a Set-Cookie value, or a name/value pair read from a Cookie header.
type Cookie struct {
	Name, Value string
	Path        string
	Domain      string
	Expires     time.Time
	MaxAge      int // 0: unset; < 0: delete now
	Secure      bool
	HttpOnly    bool
	SameSite    SameSite
}

// String formats the cookie for a Set-Cookie header (or, with only Name and
// Value set, for a Cookie header).
func (c *Cookie) String() string {
	var b strings.Builder
	b.WriteString(c.Name)
	b.WriteByte('=')
	b.WriteString(sanitizeValue(c.Value))
	if c.Path != "" {
		b.WriteString("; Path=" + c.Path)
	}
	if c.Domain != "" {
		b.WriteString("; Domain=" + strings.TrimPrefix(c.Domain, "."))
	}
	if !c.Expires.IsZero() && c.Expires.Year() >= 1601 {
		b.WriteString("; Expires=" + c.Expires.UTC().Format(TimeFormat))
	}
	switch {
	case c.MaxAge > 0:
		b.WriteString("; Max-Age=" + strconv.Itoa(c.MaxAge))
	case c.MaxAge < 0:
		b.WriteString("; Max-Age=0")
	}
	if c.HttpOnly {
		b.WriteString("; HttpOnly")
	}
	if c.Secure {
		b.WriteString("; Secure")
	}
	switch c.SameSite {
	case SameSiteNoneMode:
		b.WriteString("; SameSite=None")
	case SameSiteLaxMode:
		b.WriteString("; SameSite=Lax")
	case SameSiteStrictMode:
		b.WriteString("; SameSite=Strict")
	}
	return b.String()
}

func sanitizeValue(v string) string {
	clean := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		if b := v[i]; b >= 0x20 && b < 0x7f && b != '"' && b != ';' && b != '\\' {
			clean = append(clean, b)
		}
	}
	if strings.ContainsAny(string(clean), " ,") {
		return `"` + string(clean) + `"`
	}
	return string(clean)
}

// SetCookie adds a Set-Cookie header to the response.
func SetCookie(w ResponseWriter, c *Cookie) {
	if c.Name != "" {
		w.Header().Add("Set-Cookie", c.String())
	}
}

// ParseCookies reads the pairs of Cookie request headers.
func ParseCookies(lines []string) []*Cookie {
	var cookies []*Cookie
	for _, line := range lines {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, _ := strings.Cut(part, "=")
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if len(value) > 1 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
			cookies = append(cookies, &Cookie{Name: name, Value: value})
		}
	}
	return cookies
}
