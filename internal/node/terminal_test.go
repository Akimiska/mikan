package node

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/hostchan"
	"mikan/internal/hostchan/hosttest"
	"mikan/internal/nodeapi"
)

// serveNode serves the Node API of e on a unix socket and over mTLS and returns the panel's
// clients for both.
func serveNode(t *testing.T, e *Engine) map[string]*nodeapi.Client {
	t.Helper()
	serve := func(ln net.Listener) {
		srv := &http.Server{Handler: Handler(e, quiet()), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
	}
	sock := filepath.Join(t.TempDir(), "n.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	serve(ln)
	return map[string]*nodeapi.Client{"unix": nodeapi.NewUnixClient(sock), "mtls": mtlsNode(t, serve)}
}

func openTerm(t *testing.T, c *nodeapi.Client, grant int64) (net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return c.Terminal(ctx, nodeapi.TerminalOpen{GrantID: grant, Target: "node:2", ExpiresAt: time.Now().Add(time.Hour).Unix()})
}

// The panel's stream reaches the host's agent and back, past the node's header timeout (a
// session has no deadline of the API), over the unix socket and over the panel's mTLS alike;
// closing it ends the agent's session; a second session is refused while one is open.
func TestTerminalIsPassedToTheHostsAgent(t *testing.T) {
	e, _, dir := updateEngine(t)
	agent := hosttest.Start(t, dir)
	for name, c := range serveNode(t, e) {
		conn, err := openTerm(t, c, 4)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := agent.Next()
		if s.Req.GrantID != 4 || s.Req.Target != "node:2" {
			t.Fatalf("%s: the agent was asked %+v", name, s.Req)
		}
		kind, payload, err := hostchan.ReadFrame(conn)
		if err != nil || kind != hostchan.FrameHello || !strings.Contains(string(payload), s.Recording) {
			t.Fatalf("%s hello: %c %s %v", name, kind, payload, err)
		}
		time.Sleep(1200 * time.Millisecond)
		if err := hostchan.WriteFrame(conn, hostchan.FrameData, []byte("id\n")); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, data, err := hostchan.ReadFrame(conn); err != nil || string(data) != "echo:id\n" {
			t.Fatalf("%s echo: %q %v", name, data, err)
		}
		if err := hostchan.WriteFrame(conn, hostchan.FrameResize, hostchan.ResizePayload(120, 40)); err != nil {
			t.Fatal(err)
		}
		_, err = openTerm(t, c, 5)
		var ne *nodeapi.Error
		if !errors.As(err, &ne) || ne.Code != nodeapi.CodeTerminalBusy {
			t.Errorf("%s: a second session: %v", name, err)
		}
		conn.Close()
		select {
		case <-s.Closed():
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: closing the panel's stream left the agent's session open", name)
		}
		if got := s.Sizes(); len(got) != 1 || got[0] != [2]int{120, 40} {
			t.Errorf("%s: sizes %v", name, got)
		}
		// Let the node notice the end before the next client opens its session.
		for range 200 {
			if _, err := os.Stat(filepath.Join(dir, "support/terminal/request")); err != nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestTerminalRefusesWhatIsNotARequest(t *testing.T) {
	e, h, dir := updateEngine(t)
	hosttest.MakeLayout(t, dir)
	_ = e
	exp := time.Now().Add(time.Hour).Unix()
	query := func(grant, target string, expires int64) string {
		return "/v1/terminal?grant=" + grant + "&target=" + target + "&expires=" + strconv.FormatInt(expires, 10)
	}
	for name, c := range map[string]struct {
		path    string
		upgrade string
	}{
		"no upgrade":      {query("4", "node:2", exp), ""},
		"another upgrade": {query("4", "node:2", exp), "websocket"},
		"no grant":        {query("0", "node:2", exp), nodeapi.TerminalProtocol},
		"junk grant":      {query("x", "node:2", exp), nodeapi.TerminalProtocol},
		"bad target":      {query("4", "root", exp), nodeapi.TerminalProtocol},
		"ended":           {query("4", "node:2", time.Now().Add(-time.Minute).Unix()), nodeapi.TerminalProtocol},
		"too far":         {query("4", "node:2", time.Now().Add(72*time.Hour).Unix()), nodeapi.TerminalProtocol},
		"nothing":         {"/v1/terminal", nodeapi.TerminalProtocol},
	} {
		req := httptest.NewRequest(http.MethodPost, c.path, nil)
		if c.upgrade != "" {
			req.Header.Set("Upgrade", c.upgrade)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if _, err := os.Stat(filepath.Join(dir, "support/terminal/request")); err == nil {
			t.Fatalf("%s: a request was written for the host", name)
		}
	}
	// Not a POST: not a route.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, query("4", "node:2", exp), nil))
	if rec.Code == http.StatusOK || rec.Code == http.StatusSwitchingProtocols {
		t.Errorf("GET: %d", rec.Code)
	}
}

// A host without the agent's unit, or without the directory its units watch, answers with
// what to do, and the request does not wait for a unit that comes later.
func TestTerminalWhereTheHostHasNoAgent(t *testing.T) {
	old := hostchan.TerminalWait
	hostchan.TerminalWait = 200 * time.Millisecond
	t.Cleanup(func() { hostchan.TerminalWait = old })
	e, _, dir := updateEngine(t)
	clients := serveNode(t, e)
	c := clients["unix"]
	// No support/terminal on the host: an installer before the terminal.
	_, err := openTerm(t, c, 1)
	var ne *nodeapi.Error
	if !errors.As(err, &ne) || ne.Code != nodeapi.CodeHostUnavailable {
		t.Fatalf("no directory: %v", err)
	}
	// The directory is there, the unit is not.
	hosttest.MakeLayout(t, dir)
	_, err = openTerm(t, c, 1)
	if !errors.As(err, &ne) || ne.Code != nodeapi.CodeTerminalTimeout {
		t.Fatalf("no agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support/terminal/request")); err == nil {
		t.Error("the request waits for a unit that is not there")
	}
	// And the next one is not blocked by the failed one.
	hosttest.Start(t, dir)
	conn, err := openTerm(t, c, 2)
	if err != nil {
		t.Fatalf("after a failure: %v", err)
	}
	conn.Close()
}

func TestRecordingsAreServedFromTheHost(t *testing.T) {
	e, h, dir := updateEngine(t)
	hosttest.MakeLayout(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "support/recordings/4-1800000000.cast"), []byte("{\"version\":2}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("root only"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(secret, filepath.Join(dir, "support/recordings/5-5.cast"))
	for name, c := range serveNode(t, e) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := c.Recordings(ctx)
		if err != nil || len(list.Recordings) != 1 || list.Recordings[0].Name != "4-1800000000.cast" || list.Recordings[0].GrantID != 4 {
			t.Fatalf("%s list: %+v %v", name, list, err)
		}
		raw, err := c.Recording(ctx, "4-1800000000.cast")
		if err != nil || string(raw) != "{\"version\":2}\n" {
			t.Fatalf("%s get: %q %v", name, raw, err)
		}
		for _, bad := range []string{"5-5.cast", "9-9.cast", "..%2Fsecret", "notes.txt"} {
			_, err := c.Recording(ctx, bad)
			var ne *nodeapi.Error
			var se *nodeapi.StatusError
			if !(errors.As(err, &ne) && ne.Code == nodeapi.CodeNoRecording) && !(errors.As(err, &se) && se.Status == http.StatusNotFound) {
				t.Errorf("%s %q: %v", name, bad, err)
			}
		}
		cancel()
	}
	// The recorder form too: no path component gets through the route.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/recordings/..%2Fsecret", nil))
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "root only") {
		t.Errorf("a path went through: %d %s", rec.Code, rec.Body)
	}
}
