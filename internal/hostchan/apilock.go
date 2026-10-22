package hostchan

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"mikan/internal/fsutil"
)

// The node's API for its panel alone (DesiredState.APIAllow): the node says which networks
// its server's firewall lets through to the API port, and when the panel last reached it;
// a unit of the host (installer/src/apilock.rs) keeps the rule only while the panel keeps
// coming, so a panel that moved to an address nobody knew is not locked out for good.
//
//	apilock/request      {"id", "at", "allow": ["203.0.113.5/32", ...]}: the rule the
//	                     host puts in; "allow": null takes it away
//	apilock/seen         {"at": <unix seconds>, "from": ["203.0.113.5"]}: the node's, when
//	                     the panel last reached the API and from where
//	apilock/status.json  the host's: whether the rule is in and why not
//	apilock/lifted       the host's: `mikan api-lockdown off` was run on the server, the
//	                     node opens its listener too
const (
	apiLockDir    = "apilock"
	apiLockSeen   = "seen"
	apiLockStatus = "status.json"
	apiLockLifted = "lifted"
	// apiLockLimit is far above what either side writes (a few dozen addresses at most).
	apiLockLimit = 16 << 10
)

// APILockSeen is when the panel last reached the node's API and the addresses it came from,
// newest first.
type APILockSeen struct {
	At   int64    `json:"at"`
	From []string `json:"from,omitempty"`
}

// APILockStatus is what the host says of the rule: On while it is in the firewall, Error
// why it could not be put in or taken out, Lifted when it went because the panel stayed
// away (or `mikan api-lockdown off`). At is when the host looked, in unix seconds.
type APILockStatus struct {
	On     bool   `json:"on"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Lifted bool   `json:"lifted,omitempty"`
	At     int64  `json:"at"`
}

// RequestAPILock asks the host to let only allow through to the API port; nil takes the
// rule away. The host does it within a moment, or within a minute when the path unit is
// busy (its timer looks again).
func (c *Channel) RequestAPILock(allow []string) error {
	if c == nil || c.dir == "" {
		return ErrUnavailable
	}
	raw, err := json.Marshal(struct {
		ID    string   `json:"id"`
		At    string   `json:"at"`
		Allow []string `json:"allow"`
	}{newID(), c.now().UTC().Format(time.RFC3339), allow})
	if err != nil {
		return err
	}
	return c.put(apiLockDir, raw)
}

// WriteAPILockSeen records the panel's last contact for the host.
func (c *Channel) WriteAPILockSeen(s APILockSeen) error {
	if c == nil || c.dir == "" {
		return ErrUnavailable
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dir := filepath.Join(c.dir, apiLockDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, apiLockSeen), raw, 0o644)
}

// ReadAPILockSeen is the last contact written; false when there is none.
func (c *Channel) ReadAPILockSeen() (APILockSeen, bool) {
	var s APILockSeen
	if c == nil || c.dir == "" {
		return s, false
	}
	raw, ok := readSmall(filepath.Join(c.dir, apiLockDir, apiLockSeen), apiLockLimit)
	if !ok || json.Unmarshal(raw, &s) != nil {
		return APILockSeen{}, false
	}
	return s, true
}

// ReadAPILockStatus is what the host last said of the rule; false when it said nothing (no
// unit of the host knows the rule, or it has not run yet).
func (c *Channel) ReadAPILockStatus() (APILockStatus, bool) {
	var s APILockStatus
	if c == nil || c.dir == "" {
		return s, false
	}
	raw, ok := readSmall(filepath.Join(c.dir, apiLockDir, apiLockStatus), apiLockLimit)
	if !ok || json.Unmarshal(raw, &s) != nil {
		return APILockStatus{}, false
	}
	return s, true
}

// TakeAPILifted says whether the host's admin lifted the rule by hand since the last look,
// and takes the mark away.
func (c *Channel) TakeAPILifted() bool {
	if c == nil || c.dir == "" {
		return false
	}
	return os.Remove(filepath.Join(c.dir, apiLockDir, apiLockLifted)) == nil
}

// readSmall reads a small regular file the host wrote, not through os.ReadFile: a link or
// a FIFO planted at the path is neither followed nor waited for.
func readSmall(path string, limit int64) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > limit {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, false
	}
	return raw, true
}
