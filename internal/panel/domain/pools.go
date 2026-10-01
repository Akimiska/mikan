package domain

import (
	"context"
	"database/sql"

	"mikan/internal/panel/store/db"
)

// Traffic pools (GitHub issue #6): inbounds in a pool count to it, with a limit per user
// apart from the main quota. Users get their pool limits from the tariff, like the main
// limit, and the admin may change them per user.

// ApplyTariffPools gives the user the tariff's pool limits: pools the tariff does not
// list become unlimited. What the user used stays.
func ApplyTariffPools(ctx context.Context, q *db.Queries, userID, tariffID int64) error {
	if err := q.ClearUserPoolLimits(ctx, userID); err != nil {
		return err
	}
	ps, err := q.ListTariffPools(ctx, tariffID)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if err := q.SetUserPoolLimit(ctx, db.SetUserPoolLimitParams{UserID: userID, PoolID: p.PoolID, TrafficLimit: sql.NullInt64{Int64: p.TrafficLimit, Valid: true}}); err != nil {
			return err
		}
	}
	return nil
}

// PoolExhausted says whether the user's pool has no traffic left.
func PoolExhausted(p db.UserPool) bool {
	return p.TrafficLimit.Valid && p.UsedUp+p.UsedDown >= p.TrafficLimit.Int64
}

// ExhaustedPools are the user's pools with nothing left, by pool id.
func ExhaustedPools(ctx context.Context, q *db.Queries, userID int64) (map[int64]bool, error) {
	ps, err := q.ListUserPools(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, p := range ps {
		if PoolExhausted(p) {
			out[p.PoolID] = true
		}
	}
	return out, nil
}
