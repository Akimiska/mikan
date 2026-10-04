package infraalerts

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/autotune"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/nodesync"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

type busyNodes struct{ health nodesync.HealthView }

func (busyNodes) Activity(context.Context, int64) (nodeapi.Activity, error) {
	return nodeapi.Activity{}, nodeapi.ErrUnavailable
}
func (busyNodes) CheckTarget(context.Context, int64, nodeapi.TargetCheckRequest) (nodeapi.TargetResult, error) {
	return nodeapi.TargetResult{}, nodeapi.ErrUnavailable
}
func (busyNodes) ScanTargets(context.Context, int64, nodeapi.TargetScanRequest) (nodeapi.TargetScan, error) {
	return nodeapi.TargetScan{}, nodeapi.ErrUnavailable
}
func (b busyNodes) Health(id int64) (nodesync.HealthView, bool) { return b.health, id == 1 }

type noChanges struct{}

func (noChanges) PoliciesChanged() {}
func (noChanges) SlotsChanged()    {}

// From the node's health to the admin's chat: nginx holds 443, the tuner moves XHTTP, and
// the alert names the inbound and both ports. Off for inbound events, no alert.
func TestBusyPortMoveAlertsTheAdmin(t *testing.T) {
	st, ctx := testMonitorStore(t)
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	ins, err := st.Q.ListNodeInbounds(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	hv := nodesync.HealthView{OK: true, Ports: map[string]string{}, CheckedAt: now}
	for _, in := range ins {
		hv.Ports[in.Name] = in.Port
		ls := nodeapi.ListenerStatus{Name: in.Name, OK: true}
		if in.Name == "vless-xhttp" {
			ls = nodeapi.ListenerStatus{Name: in.Name, Error: "listen tcp :443: bind: address already in use", Code: nodeapi.ListenerAddrInUse}
		}
		hv.Listeners = append(hv.Listeners, ls)
	}
	m := New(st, set, nil, nil, nil, nil, nil, slog.Default(), clock)
	state, err := m.load(ctx) // the cursor starts after what happened before
	if err != nil {
		t.Fatal(err)
	}
	tn := autotune.New(st, set, busyNodes{hv}, noChanges{}, slog.New(slog.NewTextHandler(io.Discard, nil)), clock, autotune.DefaultOptions())
	tn.MoveBusy(ctx)

	nodes, err := st.Q.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nodeByID := map[int64]db.Node{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	byID := map[int64]db.Inbound{}
	for _, in := range ins {
		byID[in.ID] = in
	}
	off := state
	cfg := Default()
	m.autotuneEvents(ctx, &state, cfg, nodeByID, byID, "en")
	if len(state.Pending) != 1 {
		t.Fatalf("pending: %+v", state.Pending)
	}
	text := state.Pending[0].Text
	for _, want := range []string{"vless-xhttp", "from port 443 to 2053", "held by another program", nodeByID[1].Name} {
		if !strings.Contains(text, want) {
			t.Fatalf("alert %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "—") {
		t.Fatalf("alert %q has an em dash", text)
	}
	cfg.Events.Inbound = false
	m.autotuneEvents(ctx, &off, cfg, nodeByID, byID, "en")
	if len(off.Pending) != 0 || off.AutoCursor != state.AutoCursor {
		t.Fatalf("inbound events off: pending %+v, cursor %d of %d", off.Pending, off.AutoCursor, state.AutoCursor)
	}
}

// A busy port the tuner may move is given time to move before the admin hears of it; a
// port it may not move is reported as quickly as any other failure.
func TestBusyInboundWaitsForTheMove(t *testing.T) {
	if inboundFailAfter(true, true) <= inboundFailAfter(false, true) {
		t.Fatal("a movable busy port is reported as fast as any failure")
	}
	if inboundFailAfter(true, false) != inboundFailAfter(false, false) {
		t.Fatal("a busy port with automatic moves off waits for a move that never comes")
	}
}
