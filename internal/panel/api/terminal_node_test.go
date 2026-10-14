package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"mikan/internal/hostchan"
	"mikan/internal/hostchan/hosttest"
	"mikan/internal/nodeapi"
	"mikan/internal/panel/support"
)

// termNodes is the remote node as the terminal sees it: a host of its own, with its agent,
// reached the way the node's API reaches it (hostchan), or an answer made up for the test.
type termNodes struct {
	NodeRuntime
	host *hostchan.Channel

	mu      sync.Mutex
	opened  []nodeapi.TerminalOpen
	failure error
	list    nodeapi.RecordingList
	rec     []byte
	recErr  error
}

func (n *termNodes) Terminal(ctx context.Context, id int64, o nodeapi.TerminalOpen) (net.Conn, error) {
	n.mu.Lock()
	n.opened = append(n.opened, o)
	fail := n.failure
	n.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	return n.host.OpenTerminal(ctx, hostchan.TerminalRequest{GrantID: o.GrantID, Target: o.Target, ExpiresAt: time.Unix(o.ExpiresAt, 0)})
}

func (n *termNodes) Recordings(context.Context, int64) (nodeapi.RecordingList, error) {
	return n.list, n.recErr
}

func (n *termNodes) Recording(context.Context, int64, string) ([]byte, error) { return n.rec, n.recErr }

type termFixture struct {
	*hostFixture
	nodes     *termNodes
	panelHost *hosttest.Agent
	nodeHost  *hosttest.Agent
	srv       *httptest.Server
	grant     support.Created
}

// newTermFixture is a panel with a remote node (id 2) and its own node (id 1), a host agent
// for each server, and a grant of the terminal level for all three targets.
func newTermFixture(t *testing.T) *termFixture {
	t.Helper()
	nodeDir := t.TempDir()
	nodes := &termNodes{host: hostchan.New(nodeDir)}
	f := newHostFixture(t, true, func(d *Deps) {
		// A grant's end is a real time from now: the clock the terminal compares it with.
		d.Now = time.Now
		d.Support = support.New(d.Store, time.Now, d.Log)
		d.Nodes = nodes
	})
	tf := &termFixture{hostFixture: f, nodes: nodes}
	tf.panelHost = hosttest.Start(t, f.dir)

	tf.nodeHost = hosttest.Start(t, nodeDir)
	tf.srv = httptest.NewServer(f.handler)
	t.Cleanup(tf.srv.Close)
	c, err := f.deps.Support.Create(context.Background(), support.CreateInput{AdminID: 1, Hours: 2, Level: support.LevelTerminal, Targets: []string{"panel", "node:1", "node:2"}})
	if err != nil {
		t.Fatal(err)
	}
	tf.grant = c
	return tf
}

func (f *termFixture) dial(target, token string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, f.srv.URL+"/api/v1/support/terminal?target="+target, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err == nil {
		f.t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

// readUntil reads messages until pred says stop; false when the stream ended first.
func readUntil(t *testing.T, c *websocket.Conn, pred func(typ websocket.MessageType, data []byte) bool) bool {
	t.Helper()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		typ, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			return false
		}
		if pred(typ, data) {
			return true
		}
	}
}

func isControl(kind string) func(websocket.MessageType, []byte) bool {
	return func(typ websocket.MessageType, data []byte) bool {
		var m struct{ Type, Code, Reason string }
		return typ == websocket.MessageText && json.Unmarshal(data, &m) == nil && m.Type == kind
	}
}

func isText(s string) func(websocket.MessageType, []byte) bool {
	return func(typ websocket.MessageType, data []byte) bool {
		return typ == websocket.MessageBinary && strings.Contains(string(data), s)
	}
}

// A remote node's terminal goes through the node's API: the panel passes the grant, the server
// and the end of the grant, the node's host agent answers on the stream, the panel's own node
// is the panel's server, and the end of the grant closes the stream to the node too.
func TestSupportTerminalOfNodes(t *testing.T) {
	f := newTermFixture(t)
	token := f.grant.Token

	// The remote node.
	c, resp, err := f.dial("node:2", token)
	if err != nil {
		t.Fatalf("%v %v", err, resp)
	}
	sess := f.nodeHost.Next()
	if !readUntil(t, c, isControl("ready")) {
		t.Fatal("not ready")
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("hostname\n")); err != nil {
		t.Fatal(err)
	}
	if !readUntil(t, c, isText("echo:hostname\n")) {
		t.Fatal("no answer from the node's host")
	}
	f.nodes.mu.Lock()
	opened := append([]nodeapi.TerminalOpen(nil), f.nodes.opened...)
	f.nodes.mu.Unlock()
	if len(opened) != 1 || opened[0].GrantID != f.grant.Grant.ID || opened[0].Target != "node:2" || opened[0].ExpiresAt != f.grant.Grant.ExpiresAt {
		t.Fatalf("the node was asked %+v", opened)
	}
	if len(f.panelHost.Requests()) != 0 {
		t.Error("the panel's own host was asked for a remote node's session")
	}
	// Revoked: the operator is told, and the node's stream closes, which ends the host's session.
	if _, err := f.deps.Support.Revoke(context.Background(), f.grant.Grant.ID, 1, ""); err != nil {
		t.Fatal(err)
	}
	if !readUntil(t, c, isControl("end")) {
		t.Fatal("the operator is not told")
	}
	select {
	case <-sess.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("the node's session stayed open after the revocation")
	}
}

func TestSupportTerminalOfThePanelsOwnNode(t *testing.T) {
	f := newTermFixture(t)
	c, resp, err := f.dial("node:1", f.grant.Token)
	if err != nil {
		t.Fatalf("%v %v", err, resp)
	}
	sess := f.panelHost.Next()
	if sess.Req.Target != "node:1" || sess.Req.GrantID != f.grant.Grant.ID {
		t.Fatalf("the panel's host was asked %+v", sess.Req)
	}
	if !readUntil(t, c, isControl("ready")) {
		t.Fatal("not ready")
	}
	f.nodes.mu.Lock()
	n := len(f.nodes.opened)
	f.nodes.mu.Unlock()
	if n != 0 {
		t.Error("the node's API was used for the panel's own node")
	}
}

// What a node says when it cannot open a session reaches the operator as a code it can act on.
func TestSupportTerminalNodeFailures(t *testing.T) {
	f := newTermFixture(t)
	for name, c := range map[string]struct {
		err  error
		code string
	}{
		"a node before the terminal": {&nodeapi.StatusError{Method: "POST", Path: "/v1/terminal", Status: 404}, "node_too_old"},
		"a busy host":                {&nodeapi.Error{Code: nodeapi.CodeTerminalBusy}, "terminal_busy"},
		"no agent":                   {&nodeapi.Error{Code: nodeapi.CodeTerminalTimeout}, "terminal_timeout"},
		"no directory":               {&nodeapi.Error{Code: nodeapi.CodeHostUnavailable}, "host_unavailable"},
		"no answer":                  {errors.Join(nodeapi.ErrUnavailable, errors.New("connection refused to 198.51.100.7")), "node_no_answer"},
	} {
		f.nodes.mu.Lock()
		f.nodes.failure = c.err
		f.nodes.mu.Unlock()
		ws, resp, err := f.dial("node:2", f.grant.Token)
		if err != nil {
			t.Fatalf("%s: %v %v", name, err, resp)
		}
		var got struct{ Type, Code string }
		ok := readUntil(t, ws, func(typ websocket.MessageType, data []byte) bool {
			return typ == websocket.MessageText && json.Unmarshal(data, &got) == nil && got.Type == "error"
		})
		if !ok || got.Code != c.code {
			t.Errorf("%s: %+v, want %s", name, got, c.code)
		}
		_ = ws.CloseNow()
	}
	// None of them left a session in the journal or the registry: the next one opens.
	f.nodes.mu.Lock()
	f.nodes.failure = nil
	f.nodes.mu.Unlock()
	ws, resp, err := f.dial("node:2", f.grant.Token)
	if err != nil {
		t.Fatalf("%v %v", err, resp)
	}
	if !readUntil(t, ws, isControl("ready")) {
		t.Fatal("not ready after the failures")
	}
}

// The recordings of a node's server are the admin's too; what a node says of them is kept to
// what its agent makes.
func TestSupportRecordingsOfANode(t *testing.T) {
	f := newTermFixture(t)
	f.nodes.list = nodeapi.RecordingList{Recordings: []nodeapi.RecordingInfo{
		{Name: "4-1800000000.cast", GrantID: 4, At: time.Unix(1800000000, 0), Size: 12},
		{Name: "../../etc/passwd", GrantID: 4, Size: 1},
		{Name: "notes.txt", Size: 1},
	}}
	f.nodes.rec = []byte("{\"version\":2}\n")
	code, body := f.do(http.MethodGet, "/api/v1/support/recordings?source=node:2", "")
	if code != http.StatusOK || !strings.Contains(body, "4-1800000000.cast") || strings.Contains(body, "passwd") || strings.Contains(body, "notes.txt") {
		t.Fatalf("list: %d %s", code, body)
	}
	code, body = f.do(http.MethodGet, "/api/v1/support/recordings/4-1800000000.cast?source=node:2", "")
	if code != http.StatusOK || body != "{\"version\":2}\n" {
		t.Fatalf("get: %d %q", code, body)
	}
	f.nodes.recErr = &nodeapi.Error{Code: nodeapi.CodeNoRecording}
	if code, _ := f.do(http.MethodGet, "/api/v1/support/recordings/9-9.cast?source=node:2", ""); code != http.StatusNotFound {
		t.Errorf("a recording the node does not have: %d", code)
	}
	f.nodes.recErr = &nodeapi.StatusError{Status: 404}
	if code, _ := f.do(http.MethodGet, "/api/v1/support/recordings?source=node:2", ""); code != http.StatusConflict {
		t.Errorf("a node before the terminal: %d", code)
	}
	// Not for a key.
	if code, _ := f.do(http.MethodGet, "/api/v1/support/recordings?source=node:2", "full"); code != http.StatusForbidden {
		t.Errorf("a full key: %d", code)
	}
}
