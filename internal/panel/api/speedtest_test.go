package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

type speedNodes struct {
	NodeRuntime
	res nodeapi.SpeedTest
	err error
}

func (n *speedNodes) SpeedTest(context.Context, int64) (nodeapi.SpeedTest, error) { return n.res, n.err }

// A test goes into the node's history, newest first, with what a node made up kept within
// sense; a busy or old node gets its own code.
func TestNodeSpeedTests(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(st.Q, clock, nil)
	token, sess, err := sessions.Create(ctx, 1, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	nodes := &speedNodes{res: nodeapi.SpeedTest{At: now, PingMs: 12.5, JitterMs: 1.2, LossPct: 5, DownBps: 500e6, UpBps: 200e6}}
	pool := domain.NewPool(st, clock)
	handler, _, err := New(Deps{
		Version: "test", Store: st, Sessions: sessions, Now: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), UserLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), TOTP: auth.NewTOTPGuard(),
		Users: domain.NewUsers(st, pool, noChanges{}, clock), Pool: pool, Changes: noChanges{}, Nodes: nodes,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		req.Header.Set("X-CSRF-Token", sess.CsrfToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	run := "/api/v1/nodes/1/speedtest"
	if code, body := call(http.MethodPost, run); code != http.StatusOK || !strings.Contains(body, `"down_bps":500000000`) {
		t.Fatalf("run: %d %s", code, body)
	}
	nodes.res = nodeapi.SpeedTest{At: time.Unix(1, 0), PingMs: math.NaN(), LossPct: 250, DownBps: -5, UpBps: 9e18, Error: "upload: " + strings.Repeat("x", 1000)}
	if code, body := call(http.MethodPost, run); code != http.StatusOK {
		t.Fatalf("run: %d %s", code, body)
	}
	code, body := call(http.MethodGet, "/api/v1/nodes/1/speedtests")
	var hist []SpeedTestView
	if code != http.StatusOK || json.Unmarshal([]byte(body), &hist) != nil || len(hist) != 2 {
		t.Fatalf("history: %d %s", code, body)
	}
	if h := hist[0]; h.PingMs != -1 || h.LossPct != 100 || h.DownBps != 0 || h.UpBps != 1e12 || len(h.Error) != 300 || !h.At.Equal(now) {
		t.Fatalf("what a node made up, kept within sense, newest first: %+v", h)
	}
	if hist[1].PingMs != 12.5 {
		t.Fatalf("the first test: %+v", hist[1])
	}

	nodes.err = &nodeapi.Error{Code: "speed_test_busy"}
	if code, body := call(http.MethodPost, run); code != http.StatusConflict || !strings.Contains(body, "speed_test_busy") {
		t.Fatalf("busy: %d %s", code, body)
	}
	nodes.err = errors.New("node POST /v1/speedtest: status 404")
	if code, body := call(http.MethodPost, run); code != http.StatusConflict || !strings.Contains(body, "node_too_old") {
		t.Fatalf("an old node: %d %s", code, body)
	}
	if code, _ := call(http.MethodGet, "/api/v1/nodes/999/speedtests"); code != http.StatusNotFound {
		t.Fatalf("an unknown node: %d", code)
	}
}
