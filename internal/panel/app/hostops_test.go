package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/logbuf"
	"mikan/internal/nodeapi"
)

// The whole panel with the log buffer and the host's directory wired in: the logs and the
// doctor and restart of the server come to an admin only, the secret path is cut from the
// log, and the request reaches the files the host's units watch.
func TestHostOperationsOfTheWholePanel(t *testing.T) {
	dir := t.TempDir()
	logs := logbuf.New(100)
	h := newHarness(t, func(o *Options) {
		o.DataDir, o.Logs = dir, logs
		o.Log = slog.New(logs.Handler(o.Log.Handler()))
	})
	api := "/" + adminPath + "/api/v1"
	h.p.log.Warn("trouble", "path", "/"+adminPath+"/api/v1/x", "password", "hunter2hunter2")

	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/logs"}, {http.MethodPost, "/system/doctor"}, {http.MethodPost, "/system/restart"},
	} {
		if resp, _ := h.do(p.method, api+p.path, nil, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s: %d", p.method, p.path, resp.StatusCode)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}

	resp, body := h.do(http.MethodGet, api+"/logs?tail=50", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "trouble") {
		t.Fatalf("logs: %d %s", resp.StatusCode, body)
	}
	for _, s := range []string{adminPath, "hunter2hunter2"} {
		if strings.Contains(string(body), s) {
			t.Errorf("the log shows %q", s)
		}
	}

	// The host's unit takes the doctor request and answers it.
	go func() {
		req := filepath.Join(dir, "doctor", "request")
		for range 4000 {
			raw, err := os.ReadFile(req)
			if err != nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			_ = os.Remove(req)
			var r struct{ ID string }
			_ = json.Unmarshal(raw, &r)
			out, _ := json.Marshal(nodeapi.HostReport{ID: r.ID, Kind: "panel", Items: []nodeapi.HostItem{{Level: "ok", Label: "Docker", Detail: "engine 28"}}})
			_ = os.WriteFile(filepath.Join(dir, "doctor", "report.json"), out, 0o644)
			return
		}
	}()
	resp, body = h.do(http.MethodPost, api+"/system/doctor", nil, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"label":"Docker"`) {
		t.Fatalf("doctor: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodPost, api+"/system/restart", nil, csrf); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart: %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(dir, "restart", "request")); err != nil {
		t.Fatalf("no restart request for the host: %v", err)
	}
	// A browser session without the CSRF header asks for nothing.
	if resp, _ := h.do(http.MethodPost, api+"/system/restart", nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("restart without csrf: %d", resp.StatusCode)
	}
}
