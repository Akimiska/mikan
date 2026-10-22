package node

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"mikan/internal/frontpage"
	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

// Apply writes the chosen page under the core's home, named after the template; a name that
// is none of the templates writes no file, there or above it.
func TestFrontPageFile(t *testing.T) {
	dir := t.TempDir()
	e, err := Start(Options{DataDir: dir, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = e.Apply(nodeapi.DesiredState{Revision: 99}) })
	if _, err := e.Apply(nodeapi.DesiredState{Revision: 1, FrontPage: "cats"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "mihomo", "front", "cats.html")); err != nil || !bytes.Equal(got, frontpage.Page("cats")) {
		t.Fatalf("the page's file: %v", err)
	}
	if _, err := e.Apply(nodeapi.DesiredState{Revision: 2, FrontPage: "../../escape"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.html")); err == nil {
		t.Fatal("a file out of the front pages")
	}
	if files, _ := os.ReadDir(filepath.Join(dir, "mihomo", "front")); len(files) != 1 {
		t.Fatalf("front pages: %v", files)
	}
}

// The front page goes to the inbounds on an HTTP transport, VLESS and Trojan, as their
// fallback-page; a plain VLESS and the other protocols take none, and with no page none has
// it. The core's parser takes them all.
func TestFrontPageInTheConfig(t *testing.T) {
	tpl := func(y string) json.RawMessage {
		t.Helper()
		tm, err := proto.Parse(y)
		if err != nil {
			t.Fatal(err)
		}
		return tm.JSON()
	}
	st := nodeapi.DesiredState{Inbounds: []nodeapi.Inbound{
		{Name: "ws", Port: "8443", Config: tpl("type: vless\nws-path: /ws\nmikan:\n  tls: node\n")},
		{Name: "xhttp", Port: "8444", Config: tpl("type: vless\nxhttp-config:\n  path: /x\nmikan:\n  tls: node\n")},
		{Name: "grpc", Port: "8445", Config: tpl("type: trojan\ngrpc-service-name: GunService\nmikan:\n  tls: node\n")},
		{Name: "plain", Port: "8446", Config: tpl("type: vless\nmikan:\n  tls: node\n")},
		{Name: "hy2", Port: "8447", Config: tpl("type: hysteria2\n")},
	}}
	cert := proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}
	pages := func(front string) map[string]any {
		t.Helper()
		raw, rejected, err := buildConfig(st, cert, false, front)
		if err != nil || len(rejected) != 0 {
			t.Fatalf("config: %v %+v", err, rejected)
		}
		var cfg struct {
			Listeners []map[string]any `json:"listeners"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatal(err)
		}
		out := map[string]any{}
		for _, l := range cfg.Listeners {
			out[l["name"].(string)] = l["fallback-page"]
		}
		return out
	}
	got := pages("/var/lib/mikan-node/front/cats.html")
	for name, want := range map[string]bool{"ws": true, "xhttp": true, "grpc": true, "plain": false, "hy2": false} {
		if has := got[name] == "/var/lib/mikan-node/front/cats.html"; has != want || !want && got[name] != nil {
			t.Errorf("%s: fallback-page %v", name, got[name])
		}
	}
	for name, page := range pages("") {
		if page != nil {
			t.Errorf("%s: a page with none chosen: %v", name, page)
		}
	}
}
