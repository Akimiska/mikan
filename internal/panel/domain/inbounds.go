package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

var (
	ErrUnknownPreset  = errors.New("unknown_preset")
	ErrUnknownInbound = errors.New("unknown_inbound")
	ErrBadPort        = errors.New("bad_port")
	ErrNoReality      = errors.New("no_reality")
	ErrBadListen      = errors.New("bad_listen")
)

// ValidPort accepts a port ("443") or a Hysteria2 hopping range ("20000-30000").
func ValidPort(spec string) bool {
	_, _, ok := parsePort(spec)
	return ok
}

// ParseListen reads the address an inbound listens on: "" (or 0.0.0.0, ::) for every
// address, else one IPv4 or IPv6 address, e.g. 127.0.0.1 behind nginx on the same server.
func ParseListen(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" || a.IsMulticast() {
		return "", ErrBadListen
	}
	if a.IsUnspecified() {
		return "", nil
	}
	return a.Unmap().String(), nil
}

// ListenPinsPort: an inbound on an address of its own sits behind a TCP proxy (nginx
// stream, HAProxy) that forwards to its port, so the port must not move on its own. Two
// inbounds of a node still may not share a port number on different addresses: one rule
// for every check, and a bind on every address takes the port on all of them.
func ListenPinsPort(listen string) bool { return listen != "" }

// SetInboundTarget points a REALITY inbound of a node at another camouflage site: dest is
// host:port, sni the name clients send ("" = the host of dest). Only the panel's own node
// may use the panel's HTTPS (127.0.0.1:<panel port>, self-steal). The running panel
// pushes the change to the node on its next reconcile.
func SetInboundTarget(ctx context.Context, st *store.Store, nodeID int64, name, dest, sni string, now time.Time) (db.Inbound, db.Inbound, error) {
	n, err := st.Q.GetNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Inbound{}, db.Inbound{}, ErrUnknownNode
	} else if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	all, err := st.Q.ListInbounds(ctx)
	if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	existing := NodeInbounds(all, nodeID)
	i := slices.IndexFunc(existing, func(e db.Inbound) bool { return e.Name == name })
	if i < 0 {
		return db.Inbound{}, db.Inbound{}, ErrUnknownInbound
	}
	prev := existing[i]
	t, err := proto.Parse(prev.Config)
	if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	if old, _ := presets.Dest(t); old == "" {
		return db.Inbound{}, db.Inbound{}, ErrNoReality
	}
	if err := presets.SetDest(t, dest, sni); err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	var opts proto.Options
	if n.Address == "" {
		if opts.SelfStealPort, _, err = settings.Get[int](ctx, settings.New(st.Q), settings.KeyPanelPort); err != nil {
			return db.Inbound{}, db.Inbound{}, err
		}
	}
	if err := proto.Validate(t, opts); err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	next, err := st.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: prev.Port, Enabled: prev.Enabled, Config: proto.Marshal(t), DisplayName: prev.DisplayName,
		UpdatedAt: now.Unix(), ID: prev.ID})
	return prev, next, err
}

// InboundNetwork is the network the inbound's port is bound on: "tcp" or "udp".
func InboundNetwork(in db.Inbound) string {
	if t, err := proto.Parse(in.Config); err == nil {
		return t.Network()
	}
	info, _ := presets.Get(in.Preset)
	return info.Network
}

// FreeName returns base, or base-2, base-3… when an inbound already has that name.
func FreeName(existing []db.Inbound, base string) string {
	taken := map[string]bool{}
	for _, e := range existing {
		taken[e.Name] = true
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	return name
}

// AddPreset creates an inbound from a preset with fresh keys on a node, for the server
// CLI; the admin API does the same with its own error mapping and a dry run on the
// node. An empty port takes the preset's default.
func AddPreset(ctx context.Context, st *store.Store, set *settings.Settings, nodeID int64, id, port string, now time.Time) (db.Inbound, error) {
	node, err := st.Q.GetNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Inbound{}, ErrUnknownNode
	}
	if err != nil {
		return db.Inbound{}, err
	}
	info, ok := presets.Get(id)
	if !ok || id == presets.Custom {
		return db.Inbound{}, ErrUnknownPreset
	}
	if port == "" {
		port = info.Port
	}
	if !ValidPort(port) {
		return db.Inbound{}, ErrBadPort
	}
	config, err := presets.NewConfig(id, "")
	if err != nil {
		return db.Inbound{}, err
	}
	t, err := proto.Parse(config)
	if err != nil {
		return db.Inbound{}, err
	}
	var opts proto.Options
	if node.Address == "" {
		// Only the panel's own node can use the panel as its REALITY target.
		if opts.SelfStealPort, _, err = settings.Get[int](ctx, set, settings.KeyPanelPort); err != nil {
			return db.Inbound{}, err
		}
	}
	if err := proto.Validate(t, opts); err != nil {
		return db.Inbound{}, fmt.Errorf("preset %s: %w", id, err)
	}
	var row db.Inbound
	err = st.Tx(ctx, func(q *db.Queries) error {
		if err := CheckPort(ctx, q, node, port, t.Network(), PortHolder{}); err != nil {
			return err
		}
		all, err := q.ListInbounds(ctx)
		if err != nil {
			return err
		}
		row, err = q.CreateInbound(ctx, db.CreateInboundParams{NodeID: nodeID, Name: FreeName(NodeInbounds(all, nodeID), info.Name), Preset: id, Port: port,
			Config: config, CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
		return err
	})
	return row, err
}

// SetInboundPort moves a node's inbound, found by name, to another port for the server
// CLI; the admin API does the same in its update handler. Keys and the REALITY target
// stay, so clients only need to refresh the subscription. It returns the inbound before
// and after the move.
func SetInboundPort(ctx context.Context, st *store.Store, nodeID int64, name, port string, now time.Time) (db.Inbound, db.Inbound, error) {
	node, err := st.Q.GetNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Inbound{}, db.Inbound{}, ErrUnknownNode
	} else if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	if !ValidPort(port) {
		return db.Inbound{}, db.Inbound{}, ErrBadPort
	}
	var prev, next db.Inbound
	err = st.Tx(ctx, func(q *db.Queries) error {
		all, err := q.ListInbounds(ctx)
		if err != nil {
			return err
		}
		existing := NodeInbounds(all, nodeID)
		i := slices.IndexFunc(existing, func(e db.Inbound) bool { return e.Name == name })
		if i < 0 {
			return ErrUnknownInbound
		}
		prev = existing[i]
		// A disabled inbound holds no port: it is checked when it comes back on.
		if prev.Enabled != 0 {
			if err := CheckPort(ctx, q, node, port, InboundNetwork(prev), InboundHolder(prev)); err != nil {
				return err
			}
		}
		next, err = q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: prev.Enabled, Config: prev.Config, DisplayName: prev.DisplayName,
			UpdatedAt: now.Unix(), ID: prev.ID})
		return err
	})
	if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	return prev, next, nil
}
