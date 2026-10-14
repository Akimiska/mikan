package hostchan

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mikan/internal/fsutil"
	"mikan/internal/nodeapi"
)

// The support terminal: a shell on this server for the holder of a live support grant of the
// terminal level, through the host's agent (`mikan terminal-agent`, installer/src/terminal.rs).
// The agent opens a unix socket in support/ for the panel's user and speaks frames on it;
// this file is the container's side:
//
//	support/terminal/request   {"grant_id", "target", "expires_at" (unix seconds), "socket"}:
//	                           a path unit starts the agent, which takes it away, makes the
//	                           socket and waits for the connection OpenTerminal makes
//	support/terminal/revoked   the ids of grants that ended; the agent ends their sessions
//	support/recordings/        what the agent recorded, read here and never written
//
// The directories are made by mikan on the host (support/ is root's, so the panel cannot
// replace the recordings or the socket); the container does not make them: a support/ it made
// itself would be refused by the agent.

const (
	supportDir    = "support"
	terminalDir   = "terminal"
	recordingsDir = "recordings"
	revokedFile   = "revoked"
	// revokedKeep is how many grant ids `revoked` holds: the newest.
	revokedKeep = 32
	// MaxRecording is the biggest recording the panel hands out; the agent stops a session
	// before its recording gets there.
	MaxRecording = nodeapi.MaxResponse
)

// terminalPoll is how often OpenTerminal looks for the agent's socket.
const terminalPoll = 20 * time.Millisecond

// TerminalWait is how long OpenTerminal waits for the host's agent to make its socket; a
// variable so that the tests of what sits above this need not wait it out.
var TerminalWait = 10 * time.Second

var (
	// ErrTerminalBusy: a session is open on this server (one at a time).
	ErrTerminalBusy = errors.New("terminal_busy")
	// ErrTerminalTimeout: the host's agent did not come up: its unit is not installed (a
	// server updated by an older installer: mikan update brings it) or the host refused.
	ErrTerminalTimeout = errors.New("terminal_timeout")
	// ErrNoRecording: there is no such recording.
	ErrNoRecording = errors.New("no_recording")
)

// The frames of the stream with the agent: `type:u8 length:u32 payload`, length big endian.
const (
	FrameData   byte = 'D' // what the operator types / what the shell prints
	FrameResize byte = 'R' // to the agent: cols:u16 rows:u16
	FrameHello  byte = 'H' // from the agent: TerminalHello
	FrameExit   byte = 'X' // from the agent: TerminalEnd
	// MaxFrame is the biggest payload either side sends.
	MaxFrame = 64 << 10
)

// TerminalHello is the agent's first frame: the recording of this session.
type TerminalHello struct {
	Recording string `json:"recording"`
	ExpiresAt int64  `json:"expires_at"`
}

// TerminalEnd is the agent's last frame: why the session ended (closed, idle, expired,
// revoked, exit, recording, error) and the shell's exit code when it had one.
type TerminalEnd struct {
	Reason string `json:"reason"`
	Code   *int   `json:"code"`
}

// WriteFrame sends one frame.
func WriteFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame of %d bytes", len(payload))
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame; a payload beyond MaxFrame is refused before it is read.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:5])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

// ResizePayload is the payload of a FrameResize: the window in characters, each from 1 to 1000.
func ResizePayload(cols, rows int) []byte {
	clamp := func(v int) uint16 { return uint16(max(1, min(v, 1000))) }
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:2], clamp(cols))
	binary.BigEndian.PutUint16(b[2:4], clamp(rows))
	return b[:]
}

// TerminalRequest is what a session is opened for.
type TerminalRequest struct {
	GrantID int64
	// Target is "panel" or "node:<id>", as in the grant.
	Target string
	// ExpiresAt is the end of the grant: the agent ends the session then.
	ExpiresAt time.Time
}

// maxLife is the longest a request may run: a grant lasts 24 hours at most.
const maxLife = 24*time.Hour + 5*time.Minute

// ValidTarget reports whether t is the name of a server of a grant: "panel", or "node:<id>"
// with the id in its canonical form.
func ValidTarget(t string) bool {
	if t == "panel" {
		return true
	}
	rest, ok := strings.CutPrefix(t, "node:")
	id, err := strconv.ParseInt(rest, 10, 64)
	return ok && err == nil && id >= 1 && strconv.FormatInt(id, 10) == rest
}

// Check says what is wrong with the request, if anything.
func (r TerminalRequest) Check(now time.Time) error {
	switch {
	case r.GrantID < 1:
		return errors.New("no grant")
	case !ValidTarget(r.Target):
		return errors.New("not a server of a grant")
	case !r.ExpiresAt.After(now):
		return errors.New("the grant has ended")
	case r.ExpiresAt.After(now.Add(maxLife)):
		return errors.New("the end is further than a grant lasts")
	}
	return nil
}

func (c *Channel) terminalPath(parts ...string) string {
	return filepath.Join(append([]string{c.dir, supportDir}, parts...)...)
}

// termConn is a session's stream: closing it lets the host's agent end the session, and
// frees the channel for the next one.
type termConn struct {
	net.Conn
	c    *Channel
	once sync.Once
}

func (t *termConn) Close() error {
	err := t.Conn.Close()
	t.once.Do(func() { t.c.session.Store(false) })
	return err
}

// OpenTerminal asks the host for a terminal session and returns its stream, a frame stream
// (ReadFrame, WriteFrame). One session at a time (ErrTerminalBusy); ErrTerminalTimeout when
// the host's agent does not answer, ErrUnavailable when there is no directory it watches.
// Closing the stream ends the session. The caller has checked the grant.
func (c *Channel) OpenTerminal(ctx context.Context, r TerminalRequest) (net.Conn, error) {
	if c == nil || c.dir == "" {
		return nil, ErrUnavailable
	}
	if err := r.Check(c.now()); err != nil {
		return nil, err
	}
	// Not MkdirAll: the host made support/terminal/ with the right owner, and a directory made
	// here would be the panel's own, which the agent does not trust.
	if fi, err := os.Stat(c.terminalPath(terminalDir)); err != nil || !fi.IsDir() {
		return nil, ErrUnavailable
	}
	if !c.session.CompareAndSwap(false, true) {
		return nil, ErrTerminalBusy
	}
	opened := false
	defer func() {
		if !opened {
			c.session.Store(false)
		}
	}()
	sock, err := socketName()
	if err != nil {
		return nil, err
	}
	// A marker of a grant that ended earlier must not end this one (an empty or "*" one
	// would): the new session starts clean.
	if err := os.Remove(c.terminalPath(terminalDir, revokedFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"grant_id": r.GrantID, "target": r.Target, "expires_at": r.ExpiresAt.Unix(), "socket": sock})
	reqPath := c.terminalPath(terminalDir, "request")
	if err := fsutil.WriteFileAtomic(reqPath, body, 0o644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, TerminalWait)
	defer cancel()
	t := time.NewTicker(terminalPoll)
	defer t.Stop()
	sockPath := c.terminalPath(sock)
	for {
		if conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", sockPath); err == nil {
			opened = true
			return &termConn{Conn: conn, c: c}, nil
		}
		select {
		case <-ctx.Done():
			// Gone if the host never took it; nothing to remove if it did.
			_ = os.Remove(reqPath)
			if ctx.Err() == context.Canceled {
				return nil, ctx.Err()
			}
			return nil, ErrTerminalTimeout
		case <-t.C:
		}
	}
}

func socketName() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "t-" + hex.EncodeToString(b) + ".sock", nil
}

// RevokeTerminal tells the host's agent that the grant has ended: its session, if there is
// one, ends within a second even where the stream could not be closed. A host without the
// terminal directory has nothing to tell.
func (c *Channel) RevokeTerminal(grantID int64) error {
	if c == nil || c.dir == "" {
		return nil
	}
	if fi, err := os.Stat(c.terminalPath(terminalDir)); err != nil || !fi.IsDir() {
		return nil
	}
	c.revokeMu.Lock()
	defer c.revokeMu.Unlock()
	path := c.terminalPath(terminalDir, revokedFile)
	var ids []string
	if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && fi.Size() <= 4096 {
		if raw, err := os.ReadFile(path); err == nil {
			ids = strings.Fields(strings.ReplaceAll(string(raw), ",", " "))
		}
	}
	// The new id last; an id once only and the newest few: the file stays small and means the same.
	ids = append(ids, strconv.FormatInt(grantID, 10))
	seen := map[string]bool{}
	var newest []string
	for i := len(ids) - 1; i >= 0 && len(newest) < revokedKeep; i-- {
		if !seen[ids[i]] && grantIDText.MatchString(ids[i]) {
			seen[ids[i]] = true
			newest = append(newest, ids[i])
		}
	}
	slices.Reverse(newest)
	return fsutil.WriteFileAtomic(path, []byte(strings.Join(newest, "\n")+"\n"), 0o644)
}

var grantIDText = regexp.MustCompile(`^[0-9]{1,18}$`)

var recordingName = regexp.MustCompile(`^([0-9]{1,18})-([0-9]{1,12})\.cast$`)

// ValidRecordingName reports whether n is the name of a recording the agent makes
// (<grant>-<unix time>.cast): the only names the panel lists or opens.
func ValidRecordingName(n string) bool { return recordingName.MatchString(n) }

// Recordings lists the recordings of this server, the newest first. A server without the
// directory has none (an empty list); ErrUnavailable when there is no data directory.
func (c *Channel) Recordings() ([]nodeapi.RecordingInfo, error) {
	if c == nil || c.dir == "" {
		return nil, ErrUnavailable
	}
	entries, err := os.ReadDir(c.terminalPath(recordingsDir))
	if errors.Is(err, os.ErrNotExist) {
		return []nodeapi.RecordingInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]nodeapi.RecordingInfo, 0, len(entries))
	for _, e := range entries {
		m := recordingName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		grant, _ := strconv.ParseInt(m[1], 10, 64)
		at, _ := strconv.ParseInt(m[2], 10, 64)
		out = append(out, nodeapi.RecordingInfo{Name: e.Name(), GrantID: grant, At: time.Unix(at, 0).UTC(), Size: fi.Size()})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// OpenRecording opens a recording for reading and returns it with its size: a regular file
// of the agent's name, not a link, not larger than MaxRecording. ErrNoRecording when there is
// none (or it is not a recording).
func (c *Channel) OpenRecording(name string) (*os.File, int64, error) {
	if c == nil || c.dir == "" {
		return nil, 0, ErrUnavailable
	}
	if !ValidRecordingName(name) {
		return nil, 0, ErrNoRecording
	}
	path := c.terminalPath(recordingsDir, name)
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > MaxRecording {
		return nil, 0, ErrNoRecording
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, ErrNoRecording
	}
	// What was opened is what was looked at: a swap in between is no recording.
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Size() > MaxRecording {
		f.Close()
		return nil, 0, ErrNoRecording
	}
	return f, after.Size(), nil
}
