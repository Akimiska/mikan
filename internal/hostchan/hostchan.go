// Package hostchan is how the panel, or a node, asks its own server for what a container
// cannot do: run `mikan doctor` on the host and restart the containers. The container has
// no Docker socket and no shell, so it writes a request file into its data directory and a
// systemd path unit on the host (installed by mikan, like the update units) runs the
// command, which takes the request away first:
//
//	doctor/request      {"id": "<hex>", "at": "<RFC 3339>"}: `mikan doctor --requested` runs
//	                    the checks and writes doctor/report.json, the report of
//	                    `mikan doctor --json` with the same "id"
//	restart/request     the same: `mikan restart --requested` restarts the containers
//
// The host's side is not trusted with more than it is: what it writes is a small regular
// file, read without following links, cut to size and cleaned of secrets before anyone
// sees it.
package hostchan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/fsutil"
	"mikan/internal/logbuf"
	"mikan/internal/nodeapi"
)

const (
	doctorDir  = "doctor"
	restartDir = "restart"
	request    = "request"
	report     = "report.json"
	// maxReport is far above what the doctor writes (a few dozen short lines).
	maxReport = 256 << 10
	// staleRestart is how long a restart request may wait for the host: a request older than
	// this is one nobody took (the unit is not installed), and a new one replaces it.
	staleRestart = 2 * time.Minute
)

// DoctorWait is how long a caller waits for the host's report: the checks that wait for the
// network (DNS, GitHub, GHCR, a node's contact) run side by side and take about 15 s.
const DoctorWait = 60 * time.Second

var (
	// ErrUnavailable: there is no data directory the host watches (tests, UI development).
	ErrUnavailable = errors.New("host_unavailable")
	// ErrBusy: the doctor is being asked already.
	ErrBusy = errors.New("doctor_busy")
	// ErrTimeout: the host did not report in time: its unit is not installed (a server
	// updated by an older installer: mikan update brings it) or stuck.
	ErrTimeout = errors.New("doctor_timeout")
	// ErrPending: a restart was asked for and the host has not taken it yet.
	ErrPending = errors.New("restart_pending")
)

// Channel is the panel's or the node's side of the files; dir is its data directory, which
// the host sees as /opt/mikan/data/panel or /opt/mikan/data/node.
type Channel struct {
	dir  string
	now  func() time.Time
	poll time.Duration
	busy sync.Mutex

	// The support terminal (terminal.go): a session is open, and the writers of the
	// revocation marker.
	session  atomic.Bool
	revokeMu sync.Mutex
}

// New makes a channel over a data directory; "" makes one that is always unavailable.
func New(dataDir string) *Channel {
	return &Channel{dir: dataDir, now: time.Now, poll: 250 * time.Millisecond}
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return hex.EncodeToString(b)
}

func body(id string, at time.Time) []byte {
	raw, _ := json.Marshal(map[string]string{"id": id, "at": at.UTC().Format(time.RFC3339)})
	return raw
}

func (c *Channel) put(sub string, data []byte) error {
	dir := filepath.Join(c.dir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, request), data, 0o644)
}

// Doctor asks the host for `mikan doctor` and waits up to wait for its report. One question
// at a time: ErrBusy while another goes on. ErrTimeout when the host does not answer; the
// request is taken back then, so a unit that appears later does not run an old question.
func (c *Channel) Doctor(ctx context.Context, wait time.Duration) (nodeapi.HostReport, error) {
	if c == nil || c.dir == "" {
		return nodeapi.HostReport{}, ErrUnavailable
	}
	if !c.busy.TryLock() {
		return nodeapi.HostReport{}, ErrBusy
	}
	defer c.busy.Unlock()
	id := newID()
	dir := filepath.Join(c.dir, doctorDir)
	// The old report goes first: nothing left of an earlier run can be taken for this one.
	if err := os.Remove(filepath.Join(dir, report)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nodeapi.HostReport{}, err
	}
	if err := c.put(doctorDir, body(id, c.now())); err != nil {
		return nodeapi.HostReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	t := time.NewTicker(c.poll)
	defer t.Stop()
	for {
		if r, ok := readReport(filepath.Join(dir, report)); ok && r.ID == id {
			return r, nil
		}
		select {
		case <-ctx.Done():
			// Gone if the host never took it; nothing to remove if it did.
			_ = os.Remove(filepath.Join(dir, request))
			if r, ok := readReport(filepath.Join(dir, report)); ok && r.ID == id {
				return r, nil
			}
			return nodeapi.HostReport{}, ErrTimeout
		case <-t.C:
		}
	}
}

// Restart asks the host to restart the containers and returns the request's id. The host
// does it a moment later (the answer to whoever asked goes out first). ErrPending while
// an earlier request is waiting.
func (c *Channel) Restart() (string, error) {
	if c == nil || c.dir == "" {
		return "", ErrUnavailable
	}
	if fi, err := os.Lstat(filepath.Join(c.dir, restartDir, request)); err == nil && c.now().Sub(fi.ModTime()) < staleRestart {
		return "", ErrPending
	}
	id := newID()
	return id, c.put(restartDir, body(id, c.now()))
}

// readReport reads the host's report as a small regular file, cleaned: not through
// os.ReadFile, so that a link or a FIFO planted at the path is neither followed nor waited
// for. false when there is none, or it is no report.
func readReport(path string) (nodeapi.HostReport, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxReport {
		return nodeapi.HostReport{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nodeapi.HostReport{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxReport+1))
	if err != nil || len(raw) > maxReport {
		return nodeapi.HostReport{}, false
	}
	var r nodeapi.HostReport
	if json.Unmarshal(raw, &r) != nil {
		return nodeapi.HostReport{}, false
	}
	r, ok := r.Clean()
	if !ok {
		return nodeapi.HostReport{}, false
	}
	// The host cuts secrets out itself; a second pass costs nothing and the report is
	// shown to people who are not the admin.
	for i := range r.Items {
		it := &r.Items[i]
		it.Label, it.Detail, it.Fix = logbuf.Redact(it.Label), logbuf.Redact(it.Detail), logbuf.Redact(it.Fix)
	}
	return r, true
}
