package server

import (
	"bytes"
	"errors"
	"html"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"
)

// baseMarker is replaced with <base href="/<prefix>/"> so the bundle (built with
// relative asset URLs) works under a secret path chosen at install time.
const baseMarker = "<!-- mikan:base -->"

// SPA serves a Vite build: hashed files under /assets are cached forever, any other
// path falls back to the HTML entry so client-side routes survive a reload.
type SPA struct {
	files  fs.FS
	entry  []byte
	prefix atomic.Pointer[string]
}

func NewSPA(files fs.FS, entryName string) (*SPA, error) {
	entry, err := fs.ReadFile(files, entryName)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(entry, []byte(baseMarker)) {
		return nil, errors.New(entryName + " has no " + baseMarker)
	}
	s := &SPA{files: files, entry: entry}
	empty := ""
	s.prefix.Store(&empty)
	return s, nil
}

func (s *SPA) SetPrefix(p string) { s.prefix.Store(&p) }

func (s *SPA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if strings.HasPrefix(name, "assets/") || (name != "" && !strings.HasSuffix(name, ".html") && path.Ext(name) != "") {
		s.serveFile(w, r, name)
		return
	}
	base := "/" + *s.prefix.Load() + "/"
	page := bytes.Replace(s.entry, []byte(baseMarker), []byte(`<base href="`+html.EscapeString(base)+`">`), 1)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

func (s *SPA) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.files, name)
	if err != nil {
		NotFound(w)
		return
	}
	h := w.Header()
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		h.Set("Content-Type", ct)
	}
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	// Embedded files have no mtime; the zero time disables Last-Modified.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}
