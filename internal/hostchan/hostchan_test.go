package hostchan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

func fast(dir string) *Channel {
	c := New(dir)
	c.poll = 5 * time.Millisecond
	return c
}

func writeReport(t *testing.T, dir string, r nodeapi.HostReport) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doctor", "report.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// host plays the systemd unit: it takes the request away and answers it with report.
func host(t *testing.T, dir string, answer func(id string) nodeapi.HostReport) {
	t.Helper()
	go func() {
		req := filepath.Join(dir, "doctor", "request")
		for range 2000 {
			raw, err := os.ReadFile(req)
			if err != nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			_ = os.Remove(req)
			var r struct{ ID string }
			if json.Unmarshal(raw, &r) != nil {
				return
			}
			rep := answer(r.ID)
			rawRep, _ := json.Marshal(rep)
			_ = os.WriteFile(filepath.Join(dir, "doctor", "report.json"), rawRep, 0o644)
			return
		}
	}()
}

func TestDoctorReturnsTheHostsReport(t *testing.T) {
	dir := t.TempDir()
	host(t, dir, func(id string) nodeapi.HostReport {
		return nodeapi.HostReport{ID: id, Kind: "node", Version: "0.5.1.1", Items: []nodeapi.HostItem{
			{Level: "ok", Label: "Docker", Detail: "engine 28"},
			{Level: "error", Label: "Disk", Detail: "0.3 GB free", Fix: "free space"},
			{Level: "weird", Label: "X"},
		}, Errors: 99}
	})
	r, err := fast(dir).Doctor(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The counts are worked out from the items, not taken from the file; an unknown level is a warning.
	if r.Kind != "node" || len(r.Items) != 3 || r.Errors != 1 || r.Warnings != 1 || r.Items[2].Level != "warn" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "doctor", "request")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the request is left behind")
	}
}

// A host that never answers (the unit is not installed on a server updated by an old
// installer) is a timeout, and the request does not wait there for a unit that comes later.
func TestDoctorTimesOutWithoutAHost(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	_, err := fast(dir).Doctor(context.Background(), 150*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 3*time.Second {
		t.Errorf("waited %v", d)
	}
	if _, err := os.Stat(filepath.Join(dir, "doctor", "request")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the request outlived the wait")
	}
}

func TestDoctorStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fast(t.TempDir()).Doctor(ctx, time.Minute); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
}

// One question at a time: the second caller is told so at once instead of piling up.
func TestDoctorIsOneAtATime(t *testing.T) {
	dir := t.TempDir()
	c := fast(dir)
	done := make(chan error, 1)
	go func() { _, err := c.Doctor(context.Background(), 300*time.Millisecond); done <- err }()
	// The first question is on its way once its request is written.
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, "doctor", "request")); err == nil {
			break
		}
		if i > 2000 {
			t.Fatal("the first question never started")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := c.Doctor(context.Background(), time.Second); !errors.Is(err, ErrBusy) {
		t.Fatalf("second question: %v", err)
	}
	if err := <-done; !errors.Is(err, ErrTimeout) {
		t.Fatalf("first call: %v", err)
	}
}

// The report of an earlier run, or one the host wrote for another request, is not this
// run's answer.
func TestDoctorIgnoresReportsOfOtherRequests(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "doctor"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReport(t, dir, nodeapi.HostReport{ID: "old", Kind: "panel", Items: []nodeapi.HostItem{{Level: "ok", Label: "Old"}}})
	host(t, dir, func(string) nodeapi.HostReport {
		return nodeapi.HostReport{ID: "somebody-else", Kind: "panel"}
	})
	if _, err := fast(dir).Doctor(context.Background(), 200*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

// What the host wrote is shown to people who are not the admin: secrets are cut again, and
// a file that is no report, a link or a giant one is no answer.
func TestReportsAreCleaned(t *testing.T) {
	dir := t.TempDir()
	host(t, dir, func(id string) nodeapi.HostReport {
		return nodeapi.HostReport{ID: id, Kind: "panel", Items: []nodeapi.HostItem{
			{Level: "warn", Label: "Contact", Detail: "link vless://secret-user@host:443?pbk=abc and token=ms_AbCdEfGhIjKlMnOpQrSt", Fix: "run with password=hunter2hunter2"},
			{Level: "ok", Label: strings.Repeat("x", 5000), Detail: strings.Repeat("y", 5000)},
		}}
	})
	r, err := fast(dir).Doctor(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := json.Marshal(r)
	for _, s := range []string{"secret-user", "AbCdEfGhIjKl", "hunter2hunter2"} {
		if strings.Contains(string(all), s) {
			t.Errorf("the report holds %q", s)
		}
	}
	if len(r.Items[1].Label) > nodeapi.MaxHostField || len(r.Items[1].Detail) > nodeapi.MaxHostDetail {
		t.Error("fields are not clipped")
	}

	p := filepath.Join(t.TempDir(), "report.json")
	for name, content := range map[string]string{
		"not json":   "<html>",
		"no kind":    `{"id":"a","items":[]}`,
		"wrong kind": `{"id":"a","kind":"router"}`,
		"too big":    `{"id":"a","kind":"panel","items":[{"level":"ok","label":"` + strings.Repeat("z", maxReport) + `"}]}`,
	} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := readReport(p); ok {
			t.Errorf("%s was taken for a report", name)
		}
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.WriteFile(p, []byte(`{"id":"a","kind":"panel"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := readReport(link); ok {
		t.Error("a link was followed")
	}
	if _, ok := readReport(p); !ok {
		t.Error("a plain report is refused")
	}
}

func TestRestartWritesOneRequest(t *testing.T) {
	dir := t.TempDir()
	c := fast(dir)
	id, err := c.Restart()
	if err != nil || len(id) != 24 {
		t.Fatalf("id %q, err %v", id, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "restart", "request"))
	if err != nil || !strings.Contains(string(raw), id) {
		t.Fatalf("request %q, %v", raw, err)
	}
	if _, err := c.Restart(); !errors.Is(err, ErrPending) {
		t.Fatalf("second request: %v", err)
	}
	// A request nobody took for long is replaced: the unit is missing, and the admin must
	// not be locked out of asking again.
	c.now = func() time.Time { return time.Now().Add(staleRestart + time.Second) }
	if id2, err := c.Restart(); err != nil || id2 == id {
		t.Fatalf("stale request: %q, %v", id2, err)
	}
	// The host takes it, and a new one is possible at once.
	if err := os.Remove(filepath.Join(dir, "restart", "request")); err != nil {
		t.Fatal(err)
	}
	if _, err := fast(dir).Restart(); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableWithoutADirectory(t *testing.T) {
	var none *Channel
	for _, c := range []*Channel{New(""), none} {
		if _, err := c.Doctor(context.Background(), time.Second); !errors.Is(err, ErrUnavailable) {
			t.Errorf("doctor: %v", err)
		}
		if _, err := c.Restart(); !errors.Is(err, ErrUnavailable) {
			t.Errorf("restart: %v", err)
		}
	}
}
