package web

import (
	"crypto/sha256"
	"fmt"
	"github.com/rm4n0s/once-campfire-go-gina/internal/database"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"strings"
	"time"
)

func notModified(w httpx.ResponseWriter, r *httpx.Request, etag string, modified time.Time) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	fresh := false
	if value, ok := r.Header["If-None-Match"]; ok {
		for _, line := range value {
			for _, tag := range strings.Split(line, ",") {
				if strings.TrimSpace(tag) == etag || strings.TrimSpace(tag) == "*" {
					fresh = true
				}
			}
		}
	} else if since, err := httpx.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !modified.IsZero() {
		fresh = !since.Before(modified.Truncate(time.Second))
	}
	if fresh {
		w.Header().Del("Content-Type")
		w.Header().Del("Content-Length")
		w.WriteHeader(httpx.StatusNotModified)
	}
	return fresh
}
func messageFreshness(w httpx.ResponseWriter, r *httpx.Request, messages []database.Message) bool {
	parts := make([]string, 0, len(messages)+2)
	var modified time.Time
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("messages/%d-%s", m.ID, m.UpdatedAt.UTC().Format("20060102150405.000000")))
		parts[len(parts)-1] = strings.ReplaceAll(parts[len(parts)-1], ".", "")
		if m.UpdatedAt.After(modified) {
			modified = m.UpdatedAt
		}
	}
	if r.Header.Get("Turbo-Frame") != "" {
		parts = append(parts, "frame")
	}
	parts = append(parts, "messages/index")
	hash := sha256.Sum256([]byte(strings.Join(parts, "/")))
	etag := fmt.Sprintf("W/\"%x\"", hash[:16])
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", modified.UTC().Format(httpx.TimeFormat))
	w.Header().Set("Cache-Control", "max-age=0, private, must-revalidate")
	return notModified(w, r, etag, modified)
}
