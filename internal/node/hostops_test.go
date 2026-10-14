package node

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/logbuf"
	"mikan/internal/nodeapi"
)

func getJSON(t *testing.T, h http.Handler, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", path, err, rec.Body)
		}
	}
	return rec.Code
}

// The node's log goes to the panel with its secrets cut out, the newest lines last, and
// tail bounds how many: nothing a caller sends makes it read past the buffer.
func TestLogsAreTailedAndRedacted(t *testing.T) {
	e, h, _ := updateEngine(t)
	e.logs = logbuf.New(50)
	log := slog.New(e.logs.Handler(nil))
	for i := range 5 {
		log.Info("line", "n", i)
	}
	log.Warn("join failed", "token", "ms_AbCdEfGhIjKlMnOpQrStUvWx", "password", "hunter2hunter2")

	var out nodeapi.Logs
	if code := getJSON(t, h, "/v1/logs?tail=3", &out); code != http.StatusOK || len(out.Lines) != 3 {
		t.Fatalf("tail 3: %d, %+v", code, out)
	}
	last := out.Lines[2]
	if last.Level != "WARN" || !strings.Contains(last.Text, "join failed") {
		t.Fatalf("the newest line is not last: %+v", out.Lines)
	}
	for _, s := range []string{"AbCdEfGhIjKl", "hunter2hunter2"} {
		if strings.Contains(last.Text, s) {
			t.Errorf("the log holds %q", s)
		}
	}
	// No tail: a default; a huge one is the buffer's size at most.
	out = nodeapi.Logs{}
	if code := getJSON(t, h, "/v1/logs", &out); code != http.StatusOK || len(out.Lines) != 6 {
		t.Fatalf("default tail: %d, %d lines", code, len(out.Lines))
	}
	for _, bad := range []string{"0", "-1", "abc", "1e3"} {
		if code := getJSON(t, h, "/v1/logs?tail="+bad, nil); code != http.StatusBadRequest {
			t.Errorf("tail=%q: %d", bad, code)
		}
	}
	if code := getJSON(t, h, "/v1/logs?tail=999999", &out); code != http.StatusOK || len(out.Lines) > nodeapi.MaxLogTail {
		t.Errorf("a huge tail: %d, %d lines", code, len(out.Lines))
	}
}

// A node started without a buffer has no log to show, and says so with an empty list.
func TestLogsWithoutABuffer(t *testing.T) {
	_, h, _ := updateEngine(t)
	var out nodeapi.Logs
	if code := getJSON(t, h, "/v1/logs", &out); code != http.StatusOK || out.Lines == nil || len(out.Lines) != 0 {
		t.Fatalf("%d, %+v", code, out)
	}
}

// The doctor question becomes doctor/request for the host's unit, and the host's report
// comes back with its secrets cut out.
func TestDoctorGoesThroughTheHost(t *testing.T) {
	_, h, dir := updateEngine(t)
	go func() {
		req := filepath.Join(dir, "doctor", "request")
		for range 3000 {
			raw, err := os.ReadFile(req)
			if err != nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			_ = os.Remove(req)
			var r struct{ ID string }
			_ = json.Unmarshal(raw, &r)
			rep, _ := json.Marshal(nodeapi.HostReport{ID: r.ID, Kind: "node", Version: "0.5.1.1", Items: []nodeapi.HostItem{
				{Level: "ok", Label: "Docker", Detail: "engine 28"},
				{Level: "warn", Label: "Contact", Detail: "password=hunter2hunter2"},
			}})
			_ = os.WriteFile(filepath.Join(dir, "doctor", "report.json"), rep, 0o644)
			return
		}
	}()
	rec := post(h, "/v1/doctor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("doctor: %d %s", rec.Code, rec.Body)
	}
	var rep nodeapi.HostReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rep.Kind != "node" || len(rep.Items) != 2 || rep.Warnings != 1 {
		t.Fatalf("report: %v %+v", err, rep)
	}
	if strings.Contains(rec.Body.String(), "hunter2hunter2") {
		t.Error("the report holds a password")
	}
}

// A host with no unit (a node updated by an old installer) is not waited for beyond the
// limit, and the answer says what to do.
func TestDoctorTimeoutIsAnswered(t *testing.T) {
	old := doctorWait
	doctorWait = 150 * time.Millisecond
	t.Cleanup(func() { doctorWait = old })
	_, h, dir := updateEngine(t)
	rec := post(h, "/v1/doctor", "")
	var e nodeapi.Error
	if rec.Code != http.StatusGatewayTimeout || json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Code != nodeapi.CodeDoctorTimeout {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "doctor", "request")); err == nil {
		t.Error("the request is left behind for a unit that is not there")
	}
}

func TestRestartIsAsked(t *testing.T) {
	_, h, dir := updateEngine(t)
	rec := post(h, "/v1/restart", "")
	var acc nodeapi.RestartAccepted
	if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &acc) != nil || acc.ID == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "restart", "request"))
	if err != nil || !strings.Contains(string(raw), acc.ID) {
		t.Fatalf("restart/request: %q, %v", raw, err)
	}
	// Asked again before the host took the first: refused, not queued.
	var e nodeapi.Error
	rec = post(h, "/v1/restart", "")
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Code != nodeapi.CodeRestartPending {
		t.Fatalf("second: %d %s", rec.Code, rec.Body)
	}
}
