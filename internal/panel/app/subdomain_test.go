package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"mikan/internal/panel/tlscert"
)

// The subscription domain leads to the subscriptions alone: the admin panel stays on the
// panel's own address.
func TestSubDomainRouter(t *testing.T) {
	names := &tlscert.BySNI{Panel: &tlscert.Holder{}, Sub: &tlscert.Holder{}}
	h := subDomainRouter{names: names,
		sub: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("sub")) }),
		all: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("all")) })}
	serve := func(host string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Body.String()
	}
	if serve("sub.example.com") != "all" {
		t.Fatal("no subscription domain: everything as before")
	}
	names.SetName("sub.example.com")
	for host, want := range map[string]string{"sub.example.com": "sub", "Sub.Example.com:2053": "sub", "sub.example.com.": "sub",
		"panel.example.com": "all", "203.0.113.7:2053": "all", "": "all"} {
		if got := serve(host); got != want {
			t.Errorf("%q: %s, want %s", host, got, want)
		}
	}
}
