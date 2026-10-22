package storetest

import (
	"context"
	"database/sql"
	"strconv"

	"mikan/internal/panel/store/db"
)

// ServePool puts a protocol that is on, on a server of its own, into group poolID: a group
// sells and gives traffic only while a server serves it.
func ServePool(ctx context.Context, q *db.Queries, poolID int64) (db.Inbound, error) {
	name := "g" + strconv.FormatInt(poolID, 10)
	n, err := q.CreateNode(ctx, db.CreateNodeParams{Name: name, Address: name + ":25305", PublicHost: "203.0.113.1", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		return db.Inbound{}, err
	}
	in, err := q.CreateInbound(ctx, db.CreateInboundParams{NodeID: n.ID, Name: name, Preset: "vless_reality_xhttp", Port: "443", Config: "{}", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		return db.Inbound{}, err
	}
	in.PoolID = sql.NullInt64{Int64: poolID, Valid: true}
	return in, q.SetInboundPool(ctx, db.SetInboundPoolParams{PoolID: in.PoolID, ID: in.ID})
}
