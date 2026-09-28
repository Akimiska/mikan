package node

import (
	"sync"
	"time"

	"mikan/internal/nodeapi"
)

type logRing struct {
	mu    sync.Mutex
	lines []nodeapi.LogLine
	next  int
	full  bool
}

func newLogRing(n int) *logRing { return &logRing{lines: make([]nodeapi.LogLine, n)} }

func (r *logRing) add(l nodeapi.LogLine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.next] = l
	r.next = (r.next + 1) % len(r.lines)
	if r.next == 0 {
		r.full = true
	}
}

func (r *logRing) since(t time.Time) []nodeapi.LogLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ordered []nodeapi.LogLine
	if r.full {
		ordered = append(ordered, r.lines[r.next:]...)
	}
	ordered = append(ordered, r.lines[:r.next]...)
	out := make([]nodeapi.LogLine, 0, len(ordered))
	for _, l := range ordered {
		if l.Time.After(t) {
			out = append(out, l)
		}
	}
	return out
}
