package autotune

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// A port another program holds on a node's server (nginx or caddy on 443, say) keeps an
// inbound from listening at all. The node names such a listener (nodeapi.ListenerStatus
// Busy), and the tuner moves the inbound to the next free port of domain.PortPool, as it
// moves a blocked one: the same rules, event, audit entry and alert.

// ReasonBusy is the reason of a port change made because another program held the port.
const ReasonBusy = "busy"

// Why a busy inbound stays where it is (BusyMove).
const (
	BusyStale  = "stale"   // the inbound moved since its listener failed: the node is catching up
	BusyOff    = "off"     // automatic moves are off for it, or its port is not one to move
	BusyMikan  = "mikan"   // the port is mikan's own: another inbound, the relay, the panel
	BusyNoPort = "no_port" // every port of the pool is taken or was left lately
)

// BusyMove decides where inbound x goes when its listener failed on failed, a port that
// something already holds on the node's server: the first port of domain.PortPool free
// over its network on ports, the node's port map, and not in left, the ports inbounds of
// the node left lately. It returns the port, or "" and why the inbound stays.
func BusyMove(ports domain.PortMap, x db.Inbound, failed string, portOn bool, left map[string]bool) (port, why string) {
	switch {
	case x.Enabled == 0 || x.Port != failed:
		return "", BusyStale
	// Hopping ranges are the admin's, and so is a port a proxy in front forwards to.
	case !portOn || x.AutoPort == 0 || strings.Contains(x.Port, "-") || domain.ListenPinsPort(x.Listen):
		return "", BusyOff
	}
	network := domain.InboundNetwork(x)
	if _, mine := ports.Busy(x.Port, network, domain.InboundHolder(x)); mine {
		return "", BusyMikan
	}
	skip := map[string]bool{x.Port: true}
	for p := range left {
		skip[p] = true
	}
	free := FreePorts(ports, network, skip)
	if len(free) == 0 {
		return "", BusyNoPort
	}
	return free[0], ""
}

// leftPorts are the ports inbounds of node left over network within Abandon: blocked on
// the way to clients or held by another program, either way not picked again.
func (t *Tuner) leftPorts(events []db.InboundEvent, node int64, network string, now time.Time) map[string]bool {
	left := map[string]bool{}
	for _, e := range events {
		if e.NodeID == node && e.Kind == "port" && e.Network == network && now.Sub(time.Unix(e.CreatedAt, 0)) < t.o.Abandon {
			left[e.OldValue] = true
		}
	}
	return left
}

func (t *Tuner) runBusy(ctx context.Context) {
	tick := time.NewTicker(t.o.Busy)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.MoveBusy(ctx)
		}
	}
}

// MoveBusy moves every inbound whose listener failed because another program holds its
// port. It reads the nodes' last health, so it costs nothing while every listener is up.
// A new port that is busy too fails on the node's next apply and is moved from in turn;
// the ports left behind are not picked again, so the moves end when the pool does.
func (t *Tuner) MoveBusy(ctx context.Context) {
	t.busyMu.Lock()
	defer t.busyMu.Unlock()
	nodes, err := t.st.Q.ListNodes(ctx)
	if err != nil {
		t.log.Error("autotune: busy ports: nodes", "err", err)
		return
	}
	type failure struct{ name, port string }
	busy := map[int64][]failure{}
	for _, n := range nodes {
		hv, ok := t.nodes.Health(n.ID)
		if n.Enabled == 0 || !ok || !hv.OK {
			continue
		}
		for _, l := range hv.Listeners {
			// A listener is judged by the port the node ran it on; without one, the node runs a
			// state this panel has not applied yet.
			if p := hv.Ports[l.Name]; p != "" && l.Busy() {
				busy[n.ID] = append(busy[n.ID], failure{l.Name, p})
			}
		}
	}
	if len(busy) == 0 {
		t.forgetBusy(nil)
		return
	}
	now := t.now()
	portOn, err := t.set.On(ctx, settings.AutoPort)
	if err != nil {
		t.log.Error("autotune: busy ports: settings", "err", err)
		return
	}
	events, err := t.st.Q.ListInboundEventsSince(ctx, now.Add(-t.o.Abandon).Unix())
	if err != nil {
		t.log.Error("autotune: busy ports: events", "err", err)
		return
	}
	seen := map[int64]bool{}
	for _, n := range nodes {
		for _, f := range busy[n.ID] {
			// Read again for every listener: a move just made takes its new port.
			ports, err := domain.NodePorts(ctx, t.st.Q, n)
			if err != nil {
				t.log.Error("autotune: busy ports: node ports", "node", n.ID, "err", err)
				break
			}
			inbounds, err := t.st.Q.ListNodeInbounds(ctx, n.ID)
			if err != nil {
				t.log.Error("autotune: busy ports: inbounds", "node", n.ID, "err", err)
				break
			}
			i := slices.IndexFunc(inbounds, func(in db.Inbound) bool { return in.Name == f.name })
			if i < 0 {
				continue
			}
			x := inbounds[i]
			seen[x.ID] = true
			network := domain.InboundNetwork(x)
			port, why := BusyMove(ports, x, f.port, portOn, t.leftPorts(events, n.ID, network, now))
			if why != "" {
				t.noteBusy(n, x, f.port, why)
				continue
			}
			// The admin or a detector round may move it meanwhile: then their port stands.
			prev, next, err := t.inbounds.Update(ctx, x.ID, domain.InboundPatch{Port: &port, FromPort: &f.port})
			if errors.Is(err, domain.ErrInboundChanged) {
				continue
			}
			if err != nil {
				t.log.Error("autotune: busy ports: move", "node", n.ID, "inbound", x.Name, "err", err)
				continue
			}
			e := t.record(ctx, now, n, next, db.AddInboundEventParams{Kind: "port", Network: network, OldValue: prev.Port, NewValue: next.Port, Reason: ReasonBusy})
			events = append(events, e)
		}
	}
	t.forgetBusy(seen)
}

// noteBusy logs once why a busy inbound stays: the node reports it every few seconds.
func (t *Tuner) noteBusy(n db.Node, x db.Inbound, port, why string) {
	key := port + "/" + why
	t.mu.Lock()
	same := t.busy[x.ID] == key
	t.busy[x.ID] = key
	t.mu.Unlock()
	if same || why == BusyStale {
		return
	}
	t.log.Warn("autotune: the inbound's port is held by another program; it stays", "node", n.ID, "inbound", x.Name, "port", port, "why", why)
}

// forgetBusy drops what noteBusy logged for inbounds that are no longer busy.
func (t *Tuner) forgetBusy(busy map[int64]bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id := range t.busy {
		if !busy[id] {
			delete(t.busy, id)
		}
	}
}
