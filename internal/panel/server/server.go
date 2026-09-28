package server

import (
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/settings"
)

// Server routes by the first path segment: the secret admin path, the subscription
// path, or nothing. Everything else gets the same bare 404, so a scanner cannot tell
// a panel from any other HTTPS endpoint.
type Server struct {
	paths atomic.Pointer[settings.Paths]
	admin http.Handler
	sub   http.Handler
}

func New(admin, sub http.Handler) *Server {
	s := &Server{admin: admin, sub: sub}
	s.paths.Store(&settings.Paths{})
	return s
}

func (s *Server) SetPaths(p settings.Paths) { s.paths.Store(&p) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	SecurityHeaders(w.Header())
	p := r.URL.Path
	// Reject non-canonical paths ("//", "/./", "/../") instead of guessing what they mean.
	if p == "" || p[0] != '/' || (path.Clean(p) != p && path.Clean(p)+"/" != p) {
		NotFound(w)
		return
	}
	seg, rest, _ := strings.Cut(p[1:], "/")
	paths := s.paths.Load()
	switch {
	case seg != "" && paths.Admin != "" && secure.Equal(seg, paths.Admin):
		s.forward(w, r, s.admin, seg, rest, p)
	case seg != "" && paths.Sub != "" && secure.Equal(seg, paths.Sub):
		s.forward(w, r, s.sub, seg, rest, p)
	default:
		NotFound(w)
	}
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, h http.Handler, seg, rest, full string) {
	if !strings.HasPrefix(full[1+len(seg):], "/") {
		// "/<secret>" without the trailing slash: relative asset URLs need the slash.
		http.Redirect(w, r, "/"+seg+"/", http.StatusFound)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + rest
	r2.URL.RawPath = ""
	h.ServeHTTP(w, r2)
}

func SecurityHeaders(h http.Header) {
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}

func NotFound(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 Not Found\n"))
}
