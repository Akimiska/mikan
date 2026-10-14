// Package hosttest plays the host's terminal agent (`mikan terminal-agent`) for the tests of
// the packages that open a support terminal: it takes support/terminal/request the way the
// agent does, makes the unix socket, says hello, echoes what is typed, notes window sizes and
// ends its session on the revocation marker, on a closed stream or when told to. It runs no
// shell.
package hosttest

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/hostchan"
)

// Request is what the panel, or a node, asked the agent for.
type Request struct {
	GrantID   int64  `json:"grant_id"`
	Target    string `json:"target"`
	ExpiresAt int64  `json:"expires_at"`
	Socket    string `json:"socket"`
}

// Agent is the fake agent over a data directory, which must have support/terminal/ in it
// (MakeLayout).
type Agent struct {
	t   *testing.T
	dir string

	mu       sync.Mutex
	requests []Request
	sessions chan *Session
	paused   bool
}

// MakeLayout makes support/terminal/ and support/recordings/ in dir, as mikan update does.
func MakeLayout(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{"support/terminal", "support/recordings"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// Start runs the agent until the test ends.
func Start(t *testing.T, dir string) *Agent {
	t.Helper()
	MakeLayout(t, dir)
	a := &Agent{t: t, dir: dir, sessions: make(chan *Session, 8)}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go a.loop(stop)
	return a
}

// Pause makes the agent leave requests where they are, like a host whose unit is not there.
func (a *Agent) Pause(p bool) {
	a.mu.Lock()
	a.paused = p
	a.mu.Unlock()
}

// Requests are the requests the agent took, in order.
func (a *Agent) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request(nil), a.requests...)
}

// Next waits for the next session to begin.
func (a *Agent) Next() *Session {
	a.t.Helper()
	select {
	case s := <-a.sessions:
		return s
	case <-time.After(10 * time.Second):
		a.t.Fatal("no terminal session began")
		return nil
	}
}

func (a *Agent) loop(stop <-chan struct{}) {
	req := filepath.Join(a.dir, "support", "terminal", "request")
	for {
		select {
		case <-stop:
			return
		case <-time.After(3 * time.Millisecond):
		}
		a.mu.Lock()
		paused := a.paused
		a.mu.Unlock()
		if paused {
			continue
		}
		raw, err := os.ReadFile(req)
		if err != nil {
			continue
		}
		_ = os.Remove(req)
		var r Request
		if json.Unmarshal(raw, &r) != nil || !strings.HasSuffix(r.Socket, ".sock") || strings.ContainsAny(r.Socket, "/\\") {
			continue
		}
		a.mu.Lock()
		a.requests = append(a.requests, r)
		a.mu.Unlock()
		ln, err := net.Listen("unix", filepath.Join(a.dir, "support", r.Socket))
		if err != nil {
			a.t.Errorf("fake agent: %v", err)
			continue
		}
		go a.serve(ln, r, stop)
	}
}

// Session is one session as the agent sees it.
type Session struct {
	Req Request
	// Typed collects what arrived as data frames.
	mu      sync.Mutex
	typed   strings.Builder
	sizes   [][2]int
	conn    net.Conn
	closed  chan struct{}
	endWith chan string
	// Recording is the name the agent said in its hello.
	Recording string
}

// Typed is everything the other side typed so far.
func (s *Session) Typed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.typed.String()
}

// Sizes are the window sizes it was told, cols and rows.
func (s *Session) Sizes() [][2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]int(nil), s.sizes...)
}

// Closed is closed when the stream ended (the other side let go, or the session was ended).
func (s *Session) Closed() <-chan struct{} { return s.closed }

// End has the agent end the session with a reason, as the real one does at the grant's end.
func (s *Session) End(reason string) {
	select {
	case s.endWith <- reason:
	default:
	}
}

// Say sends the other side a data frame, as the shell's output.
func (s *Session) Say(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = hostchan.WriteFrame(s.conn, hostchan.FrameData, []byte(text))
}

func (a *Agent) serve(ln net.Listener, r Request, stop <-chan struct{}) {
	defer ln.Close()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	// One session: the socket goes with the connection.
	_ = os.Remove(filepath.Join(a.dir, "support", r.Socket))
	s := &Session{Req: r, conn: conn, closed: make(chan struct{}), endWith: make(chan string, 1),
		Recording: strconv.FormatInt(r.GrantID, 10) + "-" + strconv.FormatInt(time.Now().Unix(), 10) + ".cast"}
	defer conn.Close()
	defer close(s.closed)
	hello, _ := json.Marshal(hostchan.TerminalHello{Recording: s.Recording, ExpiresAt: r.ExpiresAt})
	s.mu.Lock()
	err = hostchan.WriteFrame(conn, hostchan.FrameHello, hello)
	s.mu.Unlock()
	if err != nil {
		return
	}
	a.sessions <- s
	frames := make(chan struct {
		kind    byte
		payload []byte
	}, 16)
	go func() {
		defer close(frames)
		for {
			kind, payload, err := hostchan.ReadFrame(conn)
			if err != nil {
				return
			}
			frames <- struct {
				kind    byte
				payload []byte
			}{kind, payload}
		}
	}()
	marker := filepath.Join(a.dir, "support", "terminal", "revoked")
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case reason := <-s.endWith:
			s.finish(reason)
			return
		case f, ok := <-frames:
			if !ok {
				return
			}
			switch f.kind {
			case hostchan.FrameData:
				s.mu.Lock()
				s.typed.Write(f.payload)
				_ = hostchan.WriteFrame(conn, hostchan.FrameData, append([]byte("echo:"), f.payload...))
				s.mu.Unlock()
			case hostchan.FrameResize:
				if len(f.payload) == 4 {
					s.mu.Lock()
					s.sizes = append(s.sizes, [2]int{int(f.payload[0])<<8 | int(f.payload[1]), int(f.payload[2])<<8 | int(f.payload[3])})
					s.mu.Unlock()
				}
			}
		case <-tick.C:
			if raw, err := os.ReadFile(marker); err == nil {
				for _, w := range strings.Fields(strings.ReplaceAll(string(raw), ",", " ")) {
					if w == "*" || w == strconv.FormatInt(r.GrantID, 10) {
						s.finish("revoked")
						return
					}
				}
			}
		}
	}
}

func (s *Session) finish(reason string) {
	end, _ := json.Marshal(hostchan.TerminalEnd{Reason: reason})
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = hostchan.WriteFrame(s.conn, hostchan.FrameExit, end)
}
