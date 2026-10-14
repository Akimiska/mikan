package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/hostchan"
	"mikan/internal/logbuf"
	"mikan/internal/nodeapi"
	"mikan/internal/panel/auth"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

// hostNodes is the nodes as the host operations see them: what each call returns.
type hostNodes struct {
	NodeRuntime
	logs     nodeapi.Logs
	logsErr  error
	tail     int
	rep      nodeapi.HostReport
	repErr   error
	acc      nodeapi.RestartAccepted
	accErr   error
	doctors  int
	restarts int
}

func (n *hostNodes) Logs(_ context.Context, _ int64, tail int) (nodeapi.Logs, error) {
	n.tail = tail
	return n.logs, n.logsErr
}

func (n *hostNodes) HostDoctor(context.Context, int64) (nodeapi.HostReport, error) {
	n.doctors++
	return n.rep, n.repErr
}

func (n *hostNodes) HostRestart(context.Context, int64) (nodeapi.RestartAccepted, error) {
	n.restarts++
	return n.acc, n.accErr
}

const (
	hostAdminPath = "AdminPathSecret1234"
	hostRemote    = "/api/v1/nodes/2"
)

type hostFixture struct {
	t     *testing.T
	st    *db.Queries
	dir   string
	nodes *hostNodes
	logs  *logbuf.Buffer
	log   *slog.Logger
	// do sends a request as the admin's session; as "none" with no credentials, or with the
	// given Authorization header.
	do func(method, path, as string) (int, string)
}

// newHostFixture is a panel with two nodes, the log buffer and (withHost) a directory for the
// host's units; tweak changes the deps before the panel is made.
func newHostFixture(t *testing.T, withHost bool, tweak ...func(*Deps)) *hostFixture {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	// Node 1 is the panel's own; node 2 is a remote one.
	if _, err := st.Q.CreateNode(ctx, db.CreateNodeParams{Name: "far", Address: "203.0.113.5:31234", PublicHost: "203.0.113.5", CreatedAt: now.Unix(), UpdatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	if err := settings.Set(ctx, set, settings.KeyAdminPath, hostAdminPath); err != nil {
		t.Fatal(err)
	}
	f := &hostFixture{t: t, st: st.Q, dir: t.TempDir(), nodes: &hostNodes{}, logs: logbuf.New(100)}
	f.log = slog.New(f.logs.Handler(nil))
	sessions := auth.NewSessions(st.Q, clock, nil)
	token, sess, err := sessions.Create(ctx, 1, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	pool := domain.NewPool(st, clock)
	deps := Deps{
		Version: "test", Store: st, Settings: set, Sessions: sessions, Now: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IPLimit: auth.NewLimiter(100, time.Minute, time.Minute, time.Hour), UserLimit: auth.NewLimiter(10, time.Minute, time.Minute, time.Hour), TOTP: auth.NewTOTPGuard(),
		Users: domain.NewUsers(st, pool, noChanges{}, clock), Pool: pool, Changes: noChanges{}, Nodes: f.nodes, Logs: f.logs,
	}
	if withHost {
		deps.Host = hostchan.New(f.dir)
	}
	for _, change := range tweak {
		change(&deps)
	}
	handler, _, err := New(deps)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for scope, key := range map[string]string{"full": "mk_FullKeyFullKeyFullKeyFullKey0123456789", "read": "mk_ReadKeyReadKeyReadKeyReadKey0123456789"} {
		keys[scope] = key
		if _, err := st.Q.CreateAPIKey(ctx, db.CreateAPIKeyParams{AdminID: 1, Name: scope, Prefix: key[:10], Hash: hashAPIKey(key), Scope: scope, CreatedAt: now.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	f.do = func(method, path, as string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		switch as {
		case "":
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
			req.Header.Set("X-CSRF-Token", sess.CsrfToken)
		case "nocsrf":
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		case "none":
		case "full", "read":
			req.Header.Set("Authorization", "Bearer "+keys[as])
		default:
			t.Fatalf("who is %q", as)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	return f
}

func (f *hostFixture) audited(action string) int {
	f.t.Helper()
	rows, err := f.st.ListAudit(context.Background(), db.ListAuditParams{BeforeID: 1 << 62, Lim: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, r := range rows {
		if r.Action == action {
			n++
		}
	}
	return n
}

// host plays the systemd unit of the panel's server: it takes the doctor request and
// writes the report.
func (f *hostFixture) host(rep func(id string) nodeapi.HostReport) {
	f.t.Helper()
	go func() {
		req := filepath.Join(f.dir, "doctor", "request")
		for range 4000 {
			raw, err := os.ReadFile(req)
			if err != nil {
				time.Sleep(2 * time.Millisecond)
				continue
			}
			_ = os.Remove(req)
			var r struct{ ID string }
			_ = json.Unmarshal(raw, &r)
			out, _ := json.Marshal(rep(r.ID))
			_ = os.WriteFile(filepath.Join(f.dir, "doctor", "report.json"), out, 0o644)
			return
		}
	}()
}

func shortWait(t *testing.T) {
	old := doctorWait
	doctorWait = 200 * time.Millisecond
	t.Cleanup(func() { doctorWait = old })
}

// The log of the panel reaches the page without a secret in it, whoever wrote it: a token,
// a password, a link, and the panel's own secret path, which only the panel knows.
func TestPanelLogIsRedacted(t *testing.T) {
	f := newHostFixture(t, true)
	f.log.Info("one")
	f.log.Warn("login", "password", "hunter2hunter2", "token", "ms_AbCdEfGhIjKlMnOpQrStUvWx")
	f.log.Info("request", "path", "/"+hostAdminPath+"/api/v1/users", "ip", "203.0.113.9")
	code, body := f.do(http.MethodGet, "/api/v1/logs?source=panel&tail=2", "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var v LogsView
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Source != "panel" || len(v.Lines) != 2 {
		t.Fatalf("%v %+v", err, v)
	}
	for _, s := range []string{"hunter2hunter2", "AbCdEfGhIjKl", hostAdminPath} {
		if strings.Contains(body, s) {
			t.Errorf("the page gets %q", s)
		}
	}
	if !strings.Contains(v.Lines[1].Text, "/api/v1/users") || !strings.Contains(v.Lines[1].Text, "203.0.113.9") {
		t.Errorf("too much is gone: %q", v.Lines[1].Text)
	}
	// The default source and tail.
	if code, body := f.do(http.MethodGet, "/api/v1/logs", ""); code != http.StatusOK || !strings.Contains(body, `"source":"panel"`) {
		t.Fatalf("defaults: %d %s", code, body)
	}
}

func TestLogsRefuseWhatIsNotASource(t *testing.T) {
	f := newHostFixture(t, true)
	for _, src := range []string{"nodes", "node:", "node:abc", "node:0", "node:-1", "node:1:2", "../etc", "panel2"} {
		code, body := f.do(http.MethodGet, "/api/v1/logs?source="+src, "")
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "bad_source") {
			t.Errorf("source %q: %d %s", src, code, body)
		}
	}
	if code, _ := f.do(http.MethodGet, "/api/v1/logs?source=node:99", ""); code != http.StatusNotFound {
		t.Errorf("an unknown node: %d", code)
	}
	for _, tail := range []string{"0", "2001", "-5", "x"} {
		if code, _ := f.do(http.MethodGet, "/api/v1/logs?tail="+tail, ""); code != http.StatusUnprocessableEntity {
			t.Errorf("tail %q: %d", tail, code)
		}
	}
}

// A node's log is cut again on the way: the node is a server somebody else may run.
func TestNodeLogIsRedactedAgain(t *testing.T) {
	f := newHostFixture(t, true)
	f.nodes.logs = nodeapi.Logs{Lines: []nodeapi.LogLine{
		{At: time.Unix(1, 0), Level: "INFO", Text: "joined token=ms_AbCdEfGhIjKlMnOpQrStUvWx ok"},
		{At: time.Unix(2, 0), Level: "INFO", Text: "path /" + hostAdminPath + "/x link vless://user@host:443?pbk=abc"},
	}}
	code, body := f.do(http.MethodGet, "/api/v1/logs?source=node:2&tail=50", "")
	if code != http.StatusOK || f.nodes.tail != 50 {
		t.Fatalf("%d %s (tail %d)", code, body, f.nodes.tail)
	}
	for _, s := range []string{"AbCdEfGhIjKl", hostAdminPath, "user@host"} {
		if strings.Contains(body, s) {
			t.Errorf("the page gets %q", s)
		}
	}
	if !strings.Contains(body, "joined") {
		t.Errorf("too much is gone: %s", body)
	}
	// The panel's own node answers too; an old node and a dead one have their own codes.
	if code, _ := f.do(http.MethodGet, "/api/v1/logs?source=node:1", ""); code != http.StatusOK {
		t.Errorf("local node: %d", code)
	}
	f.nodes.logsErr = &nodeapi.StatusError{Method: http.MethodGet, Path: "/v1/logs", Status: http.StatusNotFound}
	if code, body := f.do(http.MethodGet, "/api/v1/logs?source=node:2", ""); code != http.StatusConflict || !strings.Contains(body, "node_too_old") {
		t.Errorf("old node: %d %s", code, body)
	}
	f.nodes.logsErr = io.ErrUnexpectedEOF
	if code, body := f.do(http.MethodGet, "/api/v1/logs?source=node:2", ""); code != http.StatusBadGateway || !strings.Contains(body, "node_no_answer") {
		t.Errorf("dead node: %d %s", code, body)
	}
	// A node that sends more than the limit is cut to it.
	f.nodes.logsErr = nil
	f.nodes.logs = nodeapi.Logs{}
	for range nodeapi.MaxLogTail + 500 {
		f.nodes.logs.Lines = append(f.nodes.logs.Lines, nodeapi.LogLine{Level: "INFO", Text: "x"})
	}
	_, body = f.do(http.MethodGet, "/api/v1/logs?source=node:2", "")
	var v LogsView
	if err := json.Unmarshal([]byte(body), &v); err != nil || len(v.Lines) != nodeapi.MaxLogTail {
		t.Errorf("%v: %d lines", err, len(v.Lines))
	}
}

// A panel running without a buffer (development) says so.
func TestPanelWithoutALogBuffer(t *testing.T) {
	f := newHostFixture(t, true, func(d *Deps) { d.Logs = nil })
	if code, body := f.do(http.MethodGet, "/api/v1/logs", ""); code != http.StatusConflict || !strings.Contains(body, "logs_unavailable") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestDoctorOfTheServer(t *testing.T) {
	f := newHostFixture(t, true)
	f.host(func(id string) nodeapi.HostReport {
		return nodeapi.HostReport{ID: id, Kind: "panel", Version: "0.5.1.1", Items: []nodeapi.HostItem{
			{Level: "ok", Label: "Docker", Detail: "engine 28"},
			{Level: "error", Label: "Disk", Detail: "0.3 GB free token=ms_AbCdEfGhIjKlMnOpQrStUvWx", Fix: "free space"},
		}}
	})
	code, body := f.do(http.MethodPost, "/api/v1/system/doctor", "")
	var rep nodeapi.HostReport
	if code != http.StatusOK || json.Unmarshal([]byte(body), &rep) != nil || rep.Kind != "panel" || rep.Errors != 1 || len(rep.Items) != 2 {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Contains(body, "AbCdEfGhIjKl") {
		t.Error("the report holds a token")
	}
	if f.audited("system.doctor") != 1 {
		t.Errorf("audit: %d", f.audited("system.doctor"))
	}
}

// A host with no unit does not hold the request: the answer says the host did not report.
func TestDoctorTimeout(t *testing.T) {
	shortWait(t)
	f := newHostFixture(t, true)
	if code, body := f.do(http.MethodPost, "/api/v1/system/doctor", ""); code != http.StatusGatewayTimeout || !strings.Contains(body, "doctor_timeout") {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "doctor", "request")); err == nil {
		t.Error("the request is left for a unit that is not there")
	}
	// The panel's own node is the panel's server: the same answer.
	if code, body := f.do(http.MethodPost, "/api/v1/nodes/1/doctor", ""); code != http.StatusGatewayTimeout || !strings.Contains(body, "doctor_timeout") {
		t.Fatalf("local node: %d %s", code, body)
	}
	g := newHostFixture(t, false)
	if code, body := g.do(http.MethodPost, "/api/v1/system/doctor", ""); code != http.StatusConflict || !strings.Contains(body, "host_unavailable") {
		t.Fatalf("no directory: %d %s", code, body)
	}
}

func TestDoctorOfANode(t *testing.T) {
	f := newHostFixture(t, true)
	f.nodes.rep = nodeapi.HostReport{ID: "r1", Kind: "node", Version: "0.5.1.1", Items: []nodeapi.HostItem{
		{Level: "warn", Label: "Contact", Detail: "password=hunter2hunter2 and vless://user@host:443?x=1"},
	}}
	code, body := f.do(http.MethodPost, hostRemote+"/doctor", "")
	var rep nodeapi.HostReport
	if code != http.StatusOK || json.Unmarshal([]byte(body), &rep) != nil || rep.Kind != "node" || rep.Warnings != 1 {
		t.Fatalf("%d %s", code, body)
	}
	for _, s := range []string{"hunter2hunter2", "user@host"} {
		if strings.Contains(body, s) {
			t.Errorf("the report holds %q", s)
		}
	}
	if f.audited("node.doctor") != 1 || f.nodes.doctors != 1 {
		t.Errorf("audit %d, calls %d", f.audited("node.doctor"), f.nodes.doctors)
	}
	// What a node answers is no report: not shown.
	f.nodes.rep = nodeapi.HostReport{Kind: "router"}
	if code, body := f.do(http.MethodPost, hostRemote+"/doctor", ""); code != http.StatusBadGateway || !strings.Contains(body, "node_no_answer") {
		t.Errorf("nonsense: %d %s", code, body)
	}
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{&nodeapi.Error{Code: nodeapi.CodeDoctorBusy}, http.StatusConflict, "doctor_busy"},
		{&nodeapi.Error{Code: nodeapi.CodeDoctorTimeout}, http.StatusGatewayTimeout, "doctor_timeout"},
		{&nodeapi.Error{Code: nodeapi.CodeHostUnavailable}, http.StatusConflict, "host_unavailable"},
		{&nodeapi.StatusError{Method: http.MethodPost, Path: "/v1/doctor", Status: http.StatusNotFound}, http.StatusConflict, "node_too_old"},
		{io.ErrUnexpectedEOF, http.StatusBadGateway, "node_no_answer"},
	} {
		f.nodes.repErr = c.err
		if code, body := f.do(http.MethodPost, hostRemote+"/doctor", ""); code != c.status || !strings.Contains(body, c.code) {
			t.Errorf("%v: %d %s", c.err, code, body)
		}
	}
	if code, _ := f.do(http.MethodPost, "/api/v1/nodes/99/doctor", ""); code != http.StatusNotFound {
		t.Errorf("an unknown node: %d", code)
	}
}

func TestRestart(t *testing.T) {
	f := newHostFixture(t, true)
	code, body := f.do(http.MethodPost, "/api/v1/system/restart", "")
	var acc nodeapi.RestartAccepted
	if code != http.StatusAccepted || json.Unmarshal([]byte(body), &acc) != nil || acc.ID == "" {
		t.Fatalf("%d %s", code, body)
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, "restart", "request"))
	if err != nil || !strings.Contains(string(raw), acc.ID) {
		t.Fatalf("restart/request: %q, %v", raw, err)
	}
	if f.audited("system.restart") != 1 {
		t.Errorf("audit: %d", f.audited("system.restart"))
	}
	// Asked again before the host took the first: refused, and not logged as done.
	if code, body := f.do(http.MethodPost, "/api/v1/system/restart", ""); code != http.StatusConflict || !strings.Contains(body, "restart_pending") {
		t.Fatalf("second: %d %s", code, body)
	}
	if f.audited("system.restart") != 1 {
		t.Errorf("a refused restart is audited: %d", f.audited("system.restart"))
	}

	// A remote node.
	f.nodes.acc = nodeapi.RestartAccepted{ID: "abc123"}
	if code, body := f.do(http.MethodPost, hostRemote+"/restart", ""); code != http.StatusAccepted || !strings.Contains(body, "abc123") || f.nodes.restarts != 1 {
		t.Fatalf("node: %d %s", code, body)
	}
	if f.audited("node.restart") != 1 {
		t.Errorf("node audit: %d", f.audited("node.restart"))
	}
	// The panel's own node lives in the panel's containers: it has no restart of its own.
	if code, body := f.do(http.MethodPost, "/api/v1/nodes/1/restart", ""); code != http.StatusConflict || !strings.Contains(body, "local_node_restart") || f.nodes.restarts != 1 {
		t.Fatalf("local: %d %s", code, body)
	}
	f.nodes.accErr = &nodeapi.Error{Code: nodeapi.CodeRestartPending}
	if code, body := f.do(http.MethodPost, hostRemote+"/restart", ""); code != http.StatusConflict || !strings.Contains(body, "restart_pending") {
		t.Fatalf("pending: %d %s", code, body)
	}
	f.nodes.accErr = io.ErrUnexpectedEOF
	if code, body := f.do(http.MethodPost, hostRemote+"/restart", ""); code != http.StatusBadGateway || !strings.Contains(body, "node_no_answer") {
		t.Fatalf("dead: %d %s", code, body)
	}
	if f.audited("node.restart") != 1 {
		t.Errorf("a failed restart is audited: %d", f.audited("node.restart"))
	}
	if code, _ := f.do(http.MethodPost, "/api/v1/nodes/99/restart", ""); code != http.StatusNotFound {
		t.Errorf("an unknown node: %d", code)
	}
}

// Who may ask: only an admin. No credentials, a session without the CSRF header and an API
// key where the endpoint is not for keys are all refused before anything is written or asked.
func TestHostOperationsAreForTheAdmin(t *testing.T) {
	shortWait(t)
	f := newHostFixture(t, true)
	f.nodes.rep = nodeapi.HostReport{ID: "r", Kind: "node"}
	all := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/logs"},
		{http.MethodGet, "/api/v1/logs?source=node:2"},
		{http.MethodPost, "/api/v1/system/doctor"},
		{http.MethodPost, hostRemote + "/doctor"},
		{http.MethodPost, "/api/v1/system/restart"},
		{http.MethodPost, hostRemote + "/restart"},
	}
	for _, e := range all {
		if code, _ := f.do(e.method, e.path, "none"); code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s: %d", e.method, e.path, code)
		}
		if e.method == http.MethodPost {
			if code, body := f.do(e.method, e.path, "nocsrf"); code != http.StatusForbidden || !strings.Contains(body, "csrf") {
				t.Errorf("no CSRF %s %s: %d %s", e.method, e.path, code, body)
			}
		}
	}
	// A key that only reads may read the (redacted) logs and nothing more.
	if code, _ := f.do(http.MethodGet, "/api/v1/logs", "read"); code != http.StatusOK {
		t.Errorf("read key, logs: %d", code)
	}
	for _, e := range all[2:] {
		if code, body := f.do(e.method, e.path, "read"); code != http.StatusForbidden || !strings.Contains(body, "read_only_key") && !strings.Contains(body, "session_only") {
			t.Errorf("read key %s %s: %d %s", e.method, e.path, code, body)
		}
	}
	// A key that can do anything may ask for a doctor, but a restart is for a browser session
	// only: a leaked key must not be able to take the panel down.
	if code, body := f.do(http.MethodPost, hostRemote+"/doctor", "full"); code != http.StatusOK {
		t.Errorf("full key, node doctor: %d %s", code, body)
	}
	for _, p := range []string{"/api/v1/system/restart", hostRemote + "/restart"} {
		if code, body := f.do(http.MethodPost, p, "full"); code != http.StatusForbidden || !strings.Contains(body, "session_only") {
			t.Errorf("full key %s: %d %s", p, code, body)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, "restart", "request")); err == nil {
		t.Error("a refused request wrote a restart")
	}
	if f.nodes.restarts != 0 {
		t.Errorf("a refused request reached a node: %d", f.nodes.restarts)
	}
	if f.audited("system.restart")+f.audited("node.restart") != 0 {
		t.Error("a refused restart is audited as done")
	}
}
