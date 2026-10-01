package domain

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// Cascades: an inbound of node A may leave the internet through node B. B runs a
// hidden relay listener (VLESS REALITY) with a key per source node; the relay's own
// traffic leaves B directly, through B's WARP, or goes on to a further node.

var (
	ErrExitSelf  = errors.New("exit_self")  // a node cannot be its own exit
	ErrExitCycle = errors.New("exit_cycle") // the chain would come back to where it started
	ErrExitOff   = errors.New("exit_off")   // the exit node is disabled
	ErrNoPort    = errors.New("relay_no_port")
)

// maxChain bounds a cascade: more hops than this only add delay.
const maxChain = 8

// CheckExit says whether traffic of node from may leave through node to: to exists, is
// on, is not from, and the relays after it never lead back to from.
func CheckExit(ctx context.Context, q *db.Queries, from, to int64) error {
	if from == to {
		return ErrExitSelf
	}
	n, err := q.GetNode(ctx, to)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if n.Enabled == 0 {
		return ErrExitOff
	}
	seen := map[int64]bool{from: true}
	cur := to
	for range maxChain {
		if seen[cur] {
			return ErrExitCycle
		}
		seen[cur] = true
		r, err := q.GetNodeRelay(ctx, cur)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !r.ExitNodeID.Valid {
			return nil
		}
		if err != nil {
			return err
		}
		cur = r.ExitNodeID.Int64
	}
	return ErrExitCycle
}

// EnsureRelay gives node its relay listener when it has none: a free TCP port, the
// first free one of PortPool (installers open those in the firewall), else a high one.
func EnsureRelay(ctx context.Context, q *db.Queries, node db.Node, now time.Time) (db.NodeRelay, error) {
	if r, err := q.GetNodeRelay(ctx, node.ID); err == nil {
		return r, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.NodeRelay{}, err
	}
	ports, err := NodePorts(ctx, q, node)
	if err != nil {
		return db.NodeRelay{}, err
	}
	port := 0
	for _, p := range PortPool {
		if ports.Free(p, "tcp") {
			port = p
			break
		}
	}
	for i := 0; port == 0 && i < 64; i++ {
		if p := 30000 + rand.IntN(30000); ports.Free(p, "tcp") {
			port = p
		}
	}
	if port == 0 {
		return db.NodeRelay{}, ErrNoPort
	}
	reality, err := presets.NewReality(presets.DefaultDest)
	if err != nil {
		return db.NodeRelay{}, err
	}
	t := proto.Template{"type": "vless", "reality-config": reality}
	if err := proto.Validate(t, proto.Options{}); err != nil {
		return db.NodeRelay{}, err
	}
	return q.CreateNodeRelay(ctx, db.CreateNodeRelayParams{NodeID: node.ID, Port: strconv.Itoa(port), Config: proto.Marshal(t), CreatedAt: now.Unix()})
}

// RelayUser is the key node src uses at exit's relay, made on first use.
func RelayUser(ctx context.Context, q *db.Queries, exit, src int64) (string, error) {
	id, err := q.GetRelayUser(ctx, db.GetRelayUserParams{ExitNodeID: exit, SrcNodeID: src})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id = newUUID()
	if err := q.AddRelayUser(ctx, db.AddRelayUserParams{ExitNodeID: exit, SrcNodeID: src, Uuid: id}); err != nil {
		return "", err
	}
	return q.GetRelayUser(ctx, db.GetRelayUserParams{ExitNodeID: exit, SrcNodeID: src})
}

// RelayUserName is how a source node shows in the relay's users.
func RelayUserName(src int64) string { return "relay-" + strconv.FormatInt(src, 10) }
