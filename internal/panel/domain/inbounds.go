package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
)

// PortInUseError names the enabled inbound that already listens on the port.
type PortInUseError struct{ Owner string }

func (e *PortInUseError) Error() string { return "port_in_use: " + e.Owner }

// ValidPort accepts a port ("443") or a Hysteria2 hopping range ("20000-30000").
func ValidPort(spec string) bool {
	lo, hi, isRange := strings.Cut(spec, "-")
	a, err := strconv.Atoi(lo)
	if err != nil || a < 1 || a > 65535 {
		return false
	}
	if !isRange {
		return true
	}
	b, err := strconv.Atoi(hi)
	return err == nil && b > a && b <= 65535
}

// InboundNetwork is the network the inbound's port is bound on: "tcp" or "udp".
func InboundNetwork(in db.Inbound) string {
	if t, err := proto.Parse(in.Config); err == nil {
		return t.Network()
	}
	info, _ := presets.Get(in.Preset)
	return info.Network
}

// PortOwner returns the enabled inbound other than skipID that listens on port over network.
func PortOwner(existing []db.Inbound, port, network string, skipID int64) (db.Inbound, bool) {
	for _, e := range existing {
		if e.ID != skipID && e.Enabled != 0 && e.Port == port && InboundNetwork(e) == network {
			return e, true
		}
	}
	return db.Inbound{}, false
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
	all, err := st.Q.ListInbounds(ctx)
	if err != nil {
		return db.Inbound{}, err
	}
	existing := NodeInbounds(all, nodeID)
	if owner, busy := PortOwner(existing, port, t.Network(), 0); busy {
		return db.Inbound{}, &PortInUseError{Owner: owner.Name}
	}
	return st.Q.CreateInbound(ctx, db.CreateInboundParams{NodeID: nodeID, Name: FreeName(existing, info.Name), Preset: id, Port: port, Config: config,
		CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
}

// SetInboundPort moves a node's inbound, found by name, to another port for the server
// CLI; the admin API does the same in its update handler. Keys and the REALITY target
// stay, so clients only need to refresh the subscription. It returns the inbound before
// and after the move.
func SetInboundPort(ctx context.Context, st *store.Store, nodeID int64, name, port string, now time.Time) (db.Inbound, db.Inbound, error) {
	if _, err := st.Q.GetNode(ctx, nodeID); errors.Is(err, sql.ErrNoRows) {
		return db.Inbound{}, db.Inbound{}, ErrUnknownNode
	} else if err != nil {
		return db.Inbound{}, db.Inbound{}, err
	}
	if !ValidPort(port) {
		return db.Inbound{}, db.Inbound{}, ErrBadPort
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
	if owner, busy := PortOwner(existing, port, InboundNetwork(prev), prev.ID); busy && prev.Enabled != 0 {
		return db.Inbound{}, db.Inbound{}, &PortInUseError{Owner: owner.Name}
	}
	next, err := st.Q.UpdateInbound(ctx, db.UpdateInboundParams{Port: port, Enabled: prev.Enabled, Config: prev.Config, DisplayName: prev.DisplayName,
		UpdatedAt: now.Unix(), ID: prev.ID})
	return prev, next, err
}
