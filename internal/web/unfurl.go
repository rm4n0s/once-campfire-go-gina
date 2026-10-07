package web

import (
	"github.com/rm4n0s/once-campfire-go-gina/internal/database"
	"github.com/rm4n0s/once-campfire-go-gina/internal/httpx"
	"strings"
)

func (s *Server) unfurl(w httpx.ResponseWriter, r *httpx.Request, _ database.User) {
	value := r.Form.Get("url")
	if strings.TrimSpace(value) == "" {
		for key := range r.Form {
			if strings.HasPrefix(key, "url[") {
				w.WriteHeader(204)
				return
			}
		}
		httpx.Error(w, "url is required", 400)
		return
	}
	body, err := s.Unfurler.Unfurl(r.Context(), value)
	if err != nil {
		s.fail(w, err)
		return
	}
	if body == nil {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(body)
}
