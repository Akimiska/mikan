package hostchan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"mikan/internal/hostchan"
	"mikan/internal/hostchan/hosttest"
)

func TestFramesRoundTripAndAreBounded(t *testing.T) {
	var buf bytes.Buffer
	for _, f := range []struct {
		kind byte
		data string
	}{{hostchan.FrameData, "ls -l\n"}, {hostchan.FrameResize, "\x00\x50\x00\x18"}, {hostchan.FrameData, ""}} {
		if err := hostchan.WriteFrame(&buf, f.kind, []byte(f.data)); err != nil {
			t.Fatal(err)
		}
		kind, data, err := hostchan.ReadFrame(&buf)
		if err != nil || kind != f.kind || string(data) != f.data {
			t.Fatalf("%q: %c %q %v", f.data, kind, data, err)
		}
	}
	// The biggest frame goes, one byte more is refused when sent and when read.
	if err := hostchan.WriteFrame(&buf, hostchan.FrameData, make([]byte, hostchan.MaxFrame)); err != nil {
		t.Fatal(err)
	}
	if _, data, err := hostchan.ReadFrame(&buf); err != nil || len(data) != hostchan.MaxFrame {
		t.Fatalf("the biggest frame: %d %v", len(data), err)
	}
	if err := hostchan.WriteFrame(&buf, hostchan.FrameData, make([]byte, hostchan.MaxFrame+1)); err == nil {
		t.Error("a frame past the limit was sent")
	}
	big := []byte{hostchan.FrameData, 0, 1, 0, 1} // 65537 bytes announced
	if _, _, err := hostchan.ReadFrame(bytes.NewReader(big)); err == nil {
		t.Error("a frame past the limit was read")
	}
	// A cut stream is an error, not a short frame.
	if _, _, err := hostchan.ReadFrame(bytes.NewReader([]byte{hostchan.FrameData, 0, 0, 0, 5, 'a'})); err == nil {
		t.Error("a half frame was read")
	}
}

// What this side writes and reads is what the host's agent (Rust, installer/src/terminal.rs)
// writes and reads: the same bytes for a frame, the same JSON for the hello and the exit. The
// agent's tests hold the same samples.
func TestWireFormatMatchesTheAgent(t *testing.T) {
	for _, c := range []struct {
		kind byte
		data []byte
		want []byte
	}{
		{hostchan.FrameData, []byte("hi"), []byte{'D', 0, 0, 0, 2, 'h', 'i'}},
		{hostchan.FrameResize, hostchan.ResizePayload(80, 24), []byte{'R', 0, 0, 0, 4, 0, 80, 0, 24}},
		{hostchan.FrameExit, nil, []byte{'X', 0, 0, 0, 0}},
	} {
		var buf bytes.Buffer
		if err := hostchan.WriteFrame(&buf, c.kind, c.data); err != nil || !bytes.Equal(buf.Bytes(), c.want) {
			t.Errorf("%c: %v %v, want %v", c.kind, buf.Bytes(), err, c.want)
		}
	}
	var hello hostchan.TerminalHello
	if err := json.Unmarshal([]byte(`{"expires_at":1800000600,"recording":"7-1800000000.cast"}`), &hello); err != nil || hello.Recording != "7-1800000000.cast" || hello.ExpiresAt != 1800000600 {
		t.Errorf("hello: %+v %v", hello, err)
	}
	var end hostchan.TerminalEnd
	if err := json.Unmarshal([]byte(`{"code":3,"reason":"exit"}`), &end); err != nil || end.Reason != "exit" || end.Code == nil || *end.Code != 3 {
		t.Errorf("exit: %+v %v", end, err)
	}
	end = hostchan.TerminalEnd{}
	if err := json.Unmarshal([]byte(`{"code":null,"reason":"idle"}`), &end); err != nil || end.Reason != "idle" || end.Code != nil {
		t.Errorf("exit without a code: %+v %v", end, err)
	}
	// The request the agent reads: these keys, an integer end, a socket name of its shape.
	dir := t.TempDir()
	hosttest.MakeLayout(t, dir)
	short, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = hostchan.New(dir).OpenTerminal(short, hostchan.TerminalRequest{GrantID: 7, Target: "node:12", ExpiresAt: time.Now().Add(time.Hour)})
	}()
	defer func() { <-done }()
	shape := regexp.MustCompile(`^\{"expires_at":[0-9]{10},"grant_id":7,"socket":"t-[0-9a-f]{24}\.sock","target":"node:12"\}$`)
	var raw []byte
	for range 100 {
		var err error
		if raw, err = os.ReadFile(filepath.Join(dir, "support/terminal/request")); err == nil {
			break
		}
		time.Sleep(3 * time.Millisecond)
	}
	if !shape.Match(raw) {
		t.Errorf("the request is %q", raw)
	}
}

func TestResizePayloadIsClamped(t *testing.T) {
	for _, c := range []struct {
		cols, rows int
		want       []byte
	}{{80, 24, []byte{0, 80, 0, 24}}, {0, -5, []byte{0, 1, 0, 1}}, {99999, 1000, []byte{3, 232, 3, 232}}} {
		if got := hostchan.ResizePayload(c.cols, c.rows); !bytes.Equal(got, c.want) {
			t.Errorf("%dx%d: %v, want %v", c.cols, c.rows, got, c.want)
		}
	}
}

func TestTargetsAndRequests(t *testing.T) {
	for _, ok := range []string{"panel", "node:1", "node:42"} {
		if !hostchan.ValidTarget(ok) {
			t.Errorf("%q", ok)
		}
	}
	for _, bad := range []string{"", "root", "node:", "node:0", "node:01", "node:+1", "node:-1", "node:1 ", "Panel", "node:1/../2", "node:99999999999999999999"} {
		if hostchan.ValidTarget(bad) {
			t.Errorf("%q is a target", bad)
		}
	}
	now := time.Unix(1_800_000_000, 0)
	good := hostchan.TerminalRequest{GrantID: 3, Target: "panel", ExpiresAt: now.Add(time.Hour)}
	if err := good.Check(now); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*hostchan.TerminalRequest){
		"no grant":    func(r *hostchan.TerminalRequest) { r.GrantID = 0 },
		"bad target":  func(r *hostchan.TerminalRequest) { r.Target = "../x" },
		"ended":       func(r *hostchan.TerminalRequest) { r.ExpiresAt = now },
		"in the past": func(r *hostchan.TerminalRequest) { r.ExpiresAt = now.Add(-time.Minute) },
		"too far":     func(r *hostchan.TerminalRequest) { r.ExpiresAt = now.Add(25 * time.Hour) },
	} {
		r := good
		mutate(&r)
		if r.Check(now) == nil {
			t.Errorf("%s: the request was good", name)
		}
	}
	if err := (hostchan.TerminalRequest{GrantID: 1, Target: "node:2", ExpiresAt: now.Add(24 * time.Hour)}).Check(now); err != nil {
		t.Errorf("a grant's full 24 hours: %v", err)
	}
}

func open(t *testing.T, c *hostchan.Channel, grant int64, target string) (net.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := c.OpenTerminal(ctx, hostchan.TerminalRequest{GrantID: grant, Target: target, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { conn.Close() })
	return conn, nil
}

// The request is written whole for the host's agent, the stream is the agent's, and a second
// session waits for the first to end.
func TestOpenTerminalAsksTheAgent(t *testing.T) {
	dir := t.TempDir()
	agent := hosttest.Start(t, dir)
	c := hostchan.New(dir)
	// A marker of a grant that ended long ago would end this one (an empty one, all): it is cleared.
	marker := filepath.Join(dir, "support", "terminal", "revoked")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	conn, err := open(t, c, 7, "panel")
	if err != nil {
		t.Fatal(err)
	}
	s := agent.Next()
	reqs := agent.Requests()
	if len(reqs) != 1 || reqs[0].GrantID != 7 || reqs[0].Target != "panel" || !strings.HasPrefix(reqs[0].Socket, "t-") || !strings.HasSuffix(reqs[0].Socket, ".sock") {
		t.Fatalf("request: %+v", reqs)
	}
	if want := time.Now().Add(time.Hour).Unix(); reqs[0].ExpiresAt < want-5 || reqs[0].ExpiresAt > want+5 {
		t.Errorf("expires_at %d, want about %d", reqs[0].ExpiresAt, want)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Error("the old marker is left to end the new session")
	}
	kind, payload, err := hostchan.ReadFrame(conn)
	var hello hostchan.TerminalHello
	if err != nil || kind != hostchan.FrameHello || json.Unmarshal(payload, &hello) != nil || hello.Recording != s.Recording {
		t.Fatalf("hello: %c %s %v", kind, payload, err)
	}
	if err := hostchan.WriteFrame(conn, hostchan.FrameData, []byte("whoami\n")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := hostchan.ReadFrame(conn); err != nil || string(data) != "echo:whoami\n" {
		t.Fatalf("echo: %q %v", data, err)
	}
	if err := hostchan.WriteFrame(conn, hostchan.FrameResize, hostchan.ResizePayload(100, 30)); err != nil {
		t.Fatal(err)
	}
	// One session at a time: the second is refused while the first is open, and is let in after.
	if _, err := open(t, c, 8, "panel"); !errors.Is(err, hostchan.ErrTerminalBusy) {
		t.Fatalf("a second session: %v", err)
	}
	if len(agent.Requests()) != 1 {
		t.Error("the refused session left a request for the host")
	}
	conn.Close()
	select {
	case <-s.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("closing the stream did not end the session")
	}
	if got := s.Sizes(); len(got) != 1 || got[0] != [2]int{100, 30} {
		t.Errorf("sizes %v", got)
	}
	if _, err := open(t, c, 9, "node:3"); err != nil {
		t.Fatalf("after the first ended: %v", err)
	}
	// The directories are the host's: opening a session made none of its own.
	for _, p := range []string{"support/terminal/request"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
			t.Errorf("%s is left", p)
		}
	}
}

func TestOpenTerminalRefusals(t *testing.T) {
	ctx := context.Background()
	good := hostchan.TerminalRequest{GrantID: 1, Target: "panel", ExpiresAt: time.Now().Add(time.Hour)}
	// No data directory at all.
	if _, err := hostchan.New("").OpenTerminal(ctx, good); !errors.Is(err, hostchan.ErrUnavailable) {
		t.Errorf("no directory: %v", err)
	}
	var nilChan *hostchan.Channel
	if _, err := nilChan.OpenTerminal(ctx, good); !errors.Is(err, hostchan.ErrUnavailable) {
		t.Errorf("nil channel: %v", err)
	}
	// A host that has not made support/terminal (an installer before the terminal): unavailable,
	// and nothing is made for it: a support/ the panel made would be its own, not root's.
	dir := t.TempDir()
	if _, err := hostchan.New(dir).OpenTerminal(ctx, good); !errors.Is(err, hostchan.ErrUnavailable) {
		t.Errorf("no support/terminal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support")); err == nil {
		t.Error("the panel made support/ itself")
	}
	// A request that is no good never reaches the host.
	hosttest.MakeLayout(t, dir)
	c := hostchan.New(dir)
	for name, r := range map[string]hostchan.TerminalRequest{
		"no grant": {Target: "panel", ExpiresAt: good.ExpiresAt},
		"target":   {GrantID: 1, Target: "etc", ExpiresAt: good.ExpiresAt},
		"ended":    {GrantID: 1, Target: "panel", ExpiresAt: time.Now().Add(-time.Minute)},
		"too far":  {GrantID: 1, Target: "panel", ExpiresAt: time.Now().Add(48 * time.Hour)},
	} {
		if _, err := c.OpenTerminal(ctx, r); err == nil {
			t.Errorf("%s: opened", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "support/terminal/request")); err == nil {
			t.Fatalf("%s: a request was written", name)
		}
	}
	// The host does not answer (no unit): the wait ends and the request does not stay for a
	// unit that comes later. A caller that gives up gets its own error.
	short, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := c.OpenTerminal(short, good); !errors.Is(err, hostchan.ErrTerminalTimeout) {
		t.Errorf("no agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support/terminal/request")); err == nil {
		t.Error("the request waits for a unit that is not there")
	}
	gone, cancelNow := context.WithCancel(ctx)
	cancelNow()
	if _, err := c.OpenTerminal(gone, good); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled caller: %v", err)
	}
	// After all that the channel is free for a real session.
	hosttest.Start(t, dir)
	if _, err := open(t, c, 1, "panel"); err != nil {
		t.Fatalf("after the refusals: %v", err)
	}
}

func TestRevocationMarker(t *testing.T) {
	dir := t.TempDir()
	c := hostchan.New(dir)
	// Without the directory there is nothing to tell, and nothing is made.
	if err := c.RevokeTerminal(5); err != nil {
		t.Fatal(err)
	}
	if err := hostchan.New("").RevokeTerminal(5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "support")); err == nil {
		t.Fatal("support/ was made")
	}
	hosttest.MakeLayout(t, dir)
	marker := filepath.Join(dir, "support/terminal/revoked")
	read := func() []string {
		raw, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Fields(string(raw))
	}
	for _, id := range []int64{5, 6, 5} {
		if err := c.RevokeTerminal(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(); strings.Join(got, ",") != "6,5" {
		t.Errorf("marker %v, want each once and the newest last", got)
	}
	// Junk in the file (an empty line, words, a huge number) is not carried on.
	if err := os.WriteFile(marker, []byte("x\n\n7 99999999999999999999999 8,9"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.RevokeTerminal(10); err != nil {
		t.Fatal(err)
	}
	if got := read(); strings.Join(got, ",") != "7,8,9,10" {
		t.Errorf("marker %v", got)
	}
	// It stays small: the newest 32.
	for id := int64(100); id < 160; id++ {
		_ = c.RevokeTerminal(id)
	}
	if got := read(); len(got) != 32 || got[31] != "159" || got[0] != "128" {
		t.Errorf("marker %v", got)
	}
	// A planted link or directory is replaced by a regular file, not followed.
	other := filepath.Join(dir, "secret")
	if err := os.WriteFile(other, []byte("root only"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(marker)
	if err := os.Symlink(other, marker); err != nil {
		t.Fatal(err)
	}
	if err := c.RevokeTerminal(11); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(other); string(raw) != "root only" {
		t.Errorf("the target of a planted link was written: %q", raw)
	}
	if got := read(); strings.Join(got, ",") != "11" {
		t.Errorf("marker %v", got)
	}
}

func writeRecording(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "support/recordings", name), bytes.Repeat([]byte("x"), size), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingsAreListedAndOpenedSafely(t *testing.T) {
	dir := t.TempDir()
	c := hostchan.New(dir)
	// No directory: none (a server that never had a session), not an error.
	if list, err := c.Recordings(); err != nil || len(list) != 0 {
		t.Fatalf("none: %v %v", list, err)
	}
	if _, err := hostchan.New("").Recordings(); !errors.Is(err, hostchan.ErrUnavailable) {
		t.Errorf("no data directory: %v", err)
	}
	hosttest.MakeLayout(t, dir)
	writeRecording(t, dir, "7-1800000100.cast", 10)
	writeRecording(t, dir, "7-1800000500.cast", 20)
	writeRecording(t, dir, "12-1800000300.cast", 30)
	writeRecording(t, dir, "notes.txt", 5)
	writeRecording(t, dir, "7-1800000100.cast.bak", 5)
	writeRecording(t, dir, "x-1.cast", 5)
	if err := os.Mkdir(filepath.Join(dir, "support/recordings/9-9.cast"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "support/recordings/5-5.cast")); err != nil {
		t.Fatal(err)
	}
	list, err := c.Recordings()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "7-1800000500.cast,12-1800000300.cast,7-1800000100.cast" {
		t.Fatalf("listed %v: only the agent's names, regular files, the newest first", names)
	}
	if list[0].GrantID != 7 || list[0].Size != 20 || list[0].At.Unix() != 1800000500 {
		t.Errorf("%+v", list[0])
	}
	f, size, err := c.OpenRecording("12-1800000300.cast")
	if err != nil || size != 30 {
		t.Fatalf("open: %d %v", size, err)
	}
	f.Close()
	// Nothing else opens: a path, a link, a directory, an odd name, a file that is not there.
	for _, bad := range []string{"../outside", "5-5.cast", "9-9.cast", "notes.txt", "", "../../etc/passwd", "7-1800000100.cast/", "99-99.cast", "7-1.cast\x00"} {
		if f, _, err := c.OpenRecording(bad); !errors.Is(err, hostchan.ErrNoRecording) {
			if f != nil {
				f.Close()
			}
			t.Errorf("%q: %v", bad, err)
		}
	}
	// More than the panel hands out is not handed out.
	writeRecording(t, dir, "3-3.cast", hostchan.MaxRecording+1)
	if f, _, err := c.OpenRecording("3-3.cast"); !errors.Is(err, hostchan.ErrNoRecording) {
		if f != nil {
			f.Close()
		}
		t.Errorf("a recording past the limit: %v", err)
	}
}
