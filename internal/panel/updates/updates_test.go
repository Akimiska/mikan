package updates

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/release"
)

func manifest(version string) []byte {
	m := release.Manifest{Version: version, Published: time.Unix(1_800_000_000, 0).UTC(), Image: "ghcr.io/miroshka000/mikan",
		Digest: "sha256:" + strings.Repeat("a", 64), Installer: map[string]release.Asset{}, Notes: map[string]string{"en": "- x"}}
	data, _ := json.Marshal(m)
	return data
}

// The panel believes a release only with the release key's signature.
func TestFetchChecksTheSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	data := manifest("0.3.10")
	sig := release.Sign(data, priv)
	files := map[string][]byte{"/manifest.json": data, "/manifest.json.sig": []byte(sig)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	ctx := context.Background()
	m, err := fetch(srv.URL+"/manifest.json", pub)(ctx)
	if err != nil || m.Version != "0.3.10" {
		t.Fatalf("signed: %+v %v", m, err)
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := fetch(srv.URL+"/manifest.json", other)(ctx); !errors.Is(err, release.ErrSignature) {
		t.Fatalf("another key: %v", err)
	}
	files["/manifest.json"] = manifest("9.9.9")
	if _, err := fetch(srv.URL+"/manifest.json", pub)(ctx); !errors.Is(err, release.ErrSignature) {
		t.Fatalf("a swapped manifest: %v", err)
	}
	if _, err := fetch(srv.URL+"/none.json", pub)(ctx); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("no release yet: %v", err)
	}
}

// The panel and the host updater meet in files: the switch, the request, the report.
func TestHostFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1_800_000_000, 0)
	c := New(dir, "0.3.9", func(context.Context) (release.Manifest, error) {
		var m release.Manifest
		return m, json.Unmarshal(manifest("0.3.10"), &m)
	}, nil, func() time.Time { return now })
	if c.State().Available() {
		t.Fatal("nothing checked yet")
	}
	c.Check(context.Background())
	if s := c.State(); !s.Available() || s.Latest.Version != "0.3.10" || !s.CheckedAt.Equal(now) {
		t.Fatalf("checked: %+v", s)
	}
	if err := c.SetAuto(true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "update", "policy.json")); string(b) != `{"auto":true}` {
		t.Fatalf("policy: %s", b)
	}
	if _, ok := c.Requested(); ok {
		t.Fatal("no request yet")
	}
	if err := c.Request(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Requested(); !ok {
		t.Fatal("the request is there for the host")
	}
	if _, ok := c.Host(); ok {
		t.Fatal("the host has not reported")
	}
	status := `{"state":"failed","version":"0.3.10","from":"0.3.9","error":"did not start","at":"2026-10-01T03:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "update", "status.json"), []byte(status), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, ok := c.Host(); !ok || s.State != "failed" || s.Error != "did not start" {
		t.Fatalf("host: %+v", s)
	}

	off := New("", "0.3.9", nil, nil, time.Now)
	if err := off.Request(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no data dir: %v", err)
	}
}
