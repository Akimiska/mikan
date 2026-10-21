// Package frontpage holds the built-in front pages, "maintenance" and a cats clicker: what a
// stranger sees at the panel's address, or on a node's own domain, instead of a bare 404.
package frontpage

import (
	"embed"
	"net/http"
	"slices"
)

//go:embed *.html
var pages embed.FS

// Templates are the names of the built-in pages.
var Templates = []string{"maintenance", "cats"}

// Page is the built-in page called name; nil for none ("" included).
func Page(name string) []byte {
	if !slices.Contains(Templates, name) {
		return nil
	}
	b, err := pages.ReadFile(name + ".html")
	if err != nil {
		return nil
	}
	return b
}

// Serve answers "/" (and /index.html) with page, any other path with a plain 404, as a
// small site of one page does.
func Serve(w http.ResponseWriter, r *http.Request, page []byte) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		NotFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}

// NotFound is a plain 404.
func NotFound(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 Not Found\n"))
}
