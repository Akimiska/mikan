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
	file := File(name)
	if file == "" {
		return nil
	}
	b, err := pages.ReadFile(file)
	if err != nil {
		return nil
	}
	return b
}

// File is the file of the built-in page called name ("cats.html"), named from Templates and
// not from name, so a path made of it holds no name a caller sent; "" for none.
func File(name string) string {
	if i := slices.Index(Templates, name); i >= 0 {
		return Templates[i] + ".html"
	}
	return ""
}

// Serve answers "/" (and /index.html) with page, any other path with a plain 404, as a
// small site of one page does (Write).
func Serve(w http.ResponseWriter, r *http.Request, page []byte) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		NotFound(w)
		return
	}
	Write(w, r, page)
}

// Write answers r with page: GET and HEAD get it, any other method the plain 404, as
// Citrus' fallback-page answers on a node: a site of one page takes no posts, and the panel's
// address, a node's domain and its protocols' ports tell no stranger apart by how they answer.
func Write(w http.ResponseWriter, r *http.Request, page []byte) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		NotFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(page)
	}
}

// NotFound is a plain 404: the one of the panel's server, its sites and the nodes' pages.
func NotFound(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("404 Not Found\n"))
}
