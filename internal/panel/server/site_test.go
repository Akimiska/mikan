package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mikan/internal/panel/settings"
)

// A site of the admin's own answers every path the panel does not own (GitHub issue #67);
// the secret paths stay the panel's, hidden files stay hidden, and without the site the
// panel answers as before.
func TestOwnSite(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<h1>Bakery</h1>", "robots.txt": "User-agent: *\n", "404.html": "<p>no such page</p>",
		".env": "SECRET=1", "menu/index.html": "<p>menu</p>"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	panel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "panel") })
	s := New(panel, panel)
	s.SetPaths(settings.Paths{Admin: "adm1n", Sub: "s0b"})
	get := func(p string) (int, string, http.Header) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		return rec.Code, rec.Body.String(), rec.Header()
	}

	if code, body, _ := get("/"); code != http.StatusNotFound || body != "404 Not Found\n" {
		t.Fatalf("no site set: %d %q", code, body)
	}
	s.SetSite(OwnSite(dir))
	for p, want := range map[string]struct {
		code int
		body string
	}{
		"/":           {200, "<h1>Bakery</h1>"},
		"/robots.txt": {200, "User-agent: *\n"},
		"/menu/":      {200, "<p>menu</p>"},
		"/nope":       {404, "<p>no such page</p>"},
		"/.env":       {404, "<p>no such page</p>"},
		"/adm1n/":     {200, "panel"},
		"/s0b/":       {200, "panel"},
	} {
		code, body, h := get(p)
		if code != want.code || body != want.body {
			t.Errorf("%s: %d %q", p, code, body)
		}
		if p != "/adm1n/" && p != "/s0b/" && h.Get("Content-Security-Policy") != "" {
			t.Errorf("%s: the panel's policy headers set the site apart", p)
		}
	}

	s.SetSite(OwnSite(filepath.Join(dir, "missing")))
	if code, body, _ := get("/robots.txt"); code != http.StatusNotFound || body != "404 Not Found\n" {
		t.Fatalf("no site on disk: %d %q", code, body)
	}
}
