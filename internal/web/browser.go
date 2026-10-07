package web

import (
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"strings"
)

func (s *Server) browserCheck(next httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w httpx.ResponseWriter, r *httpx.Request) {
		if !s.blockBrowser(w, r) {
			next(w, r)
		}
	}
}

// ApplicationController's allow_browser runs after authentication and forgery protection.
func (s *Server) blockBrowser(w httpx.ResponseWriter, r *httpx.Request) bool {
	blocked, _ := requestAgent(r).Blocked()
	if !blocked {
		return false
	}
	frame := r.Header.Get("Turbo-Frame") != ""
	if route, _, _ := recognize(r.Method, r.URL.EscapedPath()); route != nil {
		if strings.HasPrefix(route.Endpoint, "messages#") || strings.HasPrefix(route.Endpoint, "messages/by_bots#") {
			frame = false
		}
	}
	s.render(w, r, "incompatible-browser", httpx.StatusOK, page{Frame: frame})
	return true
}
