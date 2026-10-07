package web

import (
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"strings"

	"github.com/rm4n0s/once-campfire-go-gina/internal/httpcompat"
)

func formatInput(r *httpx.Request) httpcompat.FormatInput {
	var format *string
	if value := r.PathValue("format"); value != "" {
		format = &value
	} else if r.Form.Has("format") {
		value := r.Form.Get("format")
		format = &value
	}
	return httpcompat.FormatInput{Format: format, Accept: r.Header.Get("Accept"), ContentType: r.Header.Get("Content-Type"), Path: r.URL.Path, XHR: r.Header.Get("X-Requested-With") == "XMLHttpRequest"}
}
func respondFormat(w httpx.ResponseWriter, r *httpx.Request, available ...string) string {
	input := formatInput(r)
	format, err := httpcompat.Negotiate(input, available...)
	if err != nil {
		httpx.Error(w, "Invalid MIME type", 400)
		return ""
	}
	if format == "" {
		httpx.Error(w, "Not acceptable", 406)
		return ""
	}
	if input.UsesAccept() {
		vary := w.Header().Get("Vary")
		if !strings.Contains(strings.ToLower(vary), "accept") {
			if vary != "" {
				vary += ", "
			}
			w.Header().Set("Vary", vary+"Accept")
		}
	}
	return format
}
