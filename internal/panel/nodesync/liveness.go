package nodesync

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// downAfter: failed health checks in a row (five seconds apart) before a node is told to the
// addons as down; one lost packet is not an outage. It is told up at the first check that passes.
const downAfter = 3

// liveness turns the node's health checks into the two events of its life, events.NodeDown
// and events.NodeUp, once per change however long the checks go on. What was told is kept in
// node_state (key "health/<node>": "up", or "down:<unix seconds the failures began>"), so a
// restart of the panel neither tells a node that is still down again nor loses that it came
// back. A node never told down needs no word when it answers: nothing was said to take back.
type liveness struct {
	mu    sync.Mutex
	read  bool      // told is what node_state holds
	told  string    // "", "up" or "down:<since>"
	fails int       // failed checks in a row
	since time.Time // when the run of failures began
}

func (l *liveness) down() (since int64, ok bool) {
	rest, ok := strings.CutPrefix(l.told, "down:")
	if !ok {
		return 0, false
	}
	since, err := strconv.ParseInt(rest, 10, 64)
	return since, err == nil
}

// observe takes one health check of the node. A failure to read or write what was told is
// logged and tried again with the next check.
func (s *Syncer) observe(ctx context.Context, view *HealthView) {
	// A check cut short by the panel stopping says nothing of the node.
	if ctx.Err() != nil {
		return
	}
	l := &s.alive
	l.mu.Lock()
	defer l.mu.Unlock()
	if view.OK {
		l.fails = 0
	} else {
		if l.fails == 0 {
			l.since = view.CheckedAt
		}
		l.fails++
	}
	if !view.OK && l.fails < downAfter {
		return
	}
	key := stateKeyOf("health", s.id)
	if !l.read {
		v, err := s.m.st.Q.GetNodeState(ctx, key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.log.Warn("node health: what was told is unreadable", "err", err)
			return
		}
		l.told, l.read = v, true
	}
	_, wasDown := l.down()
	switch {
	case !view.OK && !wasDown:
		l.tell(ctx, s, key, events.NodeDown, "down:"+strconv.FormatInt(l.since.Unix(), 10),
			events.Node{NodeID: s.id, Code: view.Code, Since: l.since.Unix()}, "node.down:"+strconv.FormatInt(s.id, 10)+":"+strconv.FormatInt(l.since.Unix(), 10))
	case view.OK && wasDown:
		since, _ := l.down()
		l.tell(ctx, s, key, events.NodeUp, "up",
			events.Node{NodeID: s.id, DownAt: since}, "node.up:"+strconv.FormatInt(s.id, 10)+":"+strconv.FormatInt(since, 10))
	}
}

// tell writes the change and its event on one transaction, so what is told and what is kept
// agree; the node's name is read there too. A node removed meanwhile is told of to no one.
func (l *liveness) tell(ctx context.Context, s *Syncer, key, typ, next string, data events.Node, dedupe string) {
	err := s.m.st.Tx(ctx, func(q *db.Queries) error {
		n, err := q.GetNode(ctx, s.id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		data.Name = n.Name
		if err := q.SetNodeState(ctx, db.SetNodeStateParams{Key: key, Value: next}); err != nil {
			return err
		}
		return events.Emit(ctx, q, s.m.now(), typ, 0, 0, data, dedupe)
	})
	if err != nil {
		s.log.Warn("node health: the change is not told", "event", typ, "err", err)
		return
	}
	l.told = next
	s.m.hub.Kick()
}
