package domain

import (
	"context"
	"database/sql"
	"errors"

	"mikan/internal/panel/model"
	"mikan/internal/panel/store/db"
)

// Server groups (the traffic pools of GitHub issue #6): a group is a set of inbounds, and a
// tariff and a subscription hold each group in one mode. Users get the modes of their
// tariff, like its other limits, and the admin may change them per user. The inbounds in no
// group are the main servers: always in, on the main traffic.

// The modes of a group in a tariff or a subscription.
const (
	PoolMain      = model.PoolMain      // in, its traffic counts to the main traffic
	PoolOwn       = model.PoolOwn       // in, with a limit of its own apart from the main traffic
	PoolUnlimited = model.PoolUnlimited // in, without a limit, apart from the main traffic
	PoolClosed    = model.PoolClosed    // not in: left out of the subscription, the nodes turn the user away
)

// ValidPoolMode says whether m is one of the modes.
func ValidPoolMode(m string) bool {
	switch m {
	case PoolMain, PoolOwn, PoolUnlimited, PoolClosed:
		return true
	}
	return false
}

// UserPoolOf is the user's row of a group in rows, and its mode: a group without a row is in
// the main traffic.
func UserPoolOf(rows []db.UserPool, poolID int64) (db.UserPool, string) {
	for _, r := range rows {
		if r.PoolID == poolID {
			return r, r.Mode
		}
	}
	return db.UserPool{PoolID: poolID}, PoolMain
}

// TariffPoolMode is the tariff's mode of a group in rows (the tariff's own), the main traffic
// without a row.
func TariffPoolMode(rows []db.TariffPool, poolID int64) (string, sql.NullInt64) {
	for _, r := range rows {
		if r.PoolID == poolID {
			return r.Mode, r.TrafficLimit
		}
	}
	return PoolMain, sql.NullInt64{}
}

// ApplyTariffPools gives the user the tariff's mode of every group: the groups the tariff
// has no row for count to the main traffic. What the user used stays.
func ApplyTariffPools(ctx context.Context, q *db.Queries, userID, tariffID int64) error {
	if err := q.ResetUserPoolModes(ctx, userID); err != nil {
		return err
	}
	ps, err := q.ListTariffPools(ctx, tariffID)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.Mode == PoolMain {
			continue
		}
		if err := q.SetUserPool(ctx, db.SetUserPoolParams{UserID: userID, PoolID: p.PoolID, Mode: p.Mode, TrafficLimit: p.TrafficLimit}); err != nil {
			return err
		}
	}
	return GroupGrantsToMain(ctx, q, userID)
}

// PoolNames are the server groups' names by id.
func PoolNames(ctx context.Context, q *db.Queries) (map[int64]string, error) {
	pools, err := q.ListTrafficPools(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(pools))
	for _, p := range pools {
		out[p.ID] = p.Name
	}
	return out, nil
}

// GroupGrantsToMain moves what is left of the traffic bought for a server group that is no
// longer the user's own limit to the user's main traffic, on q's transaction: paid for, it
// would otherwise wait unspent in a group that counts to the main traffic, has no limit or
// is closed. Called wherever a group's mode for a user changes.
func GroupGrantsToMain(ctx context.Context, q *db.Queries, userIDs ...int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	return q.MoveGroupGrantsToMain(ctx, userIDs)
}

// TariffParts are the parts of a tariff ApplyTariffToAll brings its users up to.
type TariffParts struct {
	Traffic bool // the traffic per period and when it resets
	Devices bool // the device limit (with the devices bought on top) and how it counts
	Speed   bool // the speed
	Groups  bool // the server groups' modes and limits
}

// Any says whether anything is to be applied.
func (p TariffParts) Any() bool { return p.Traffic || p.Devices || p.Speed || p.Groups }

// ApplyTariffToAll gives every user of the tariff the chosen parts of it now, as a renewal
// would: the tariff's values replace the users' own, what they used and their terms stay.
// Changing a tariff leaves the subscriptions given out alone; this brings them up to it, so
// that the packages it sells are offered to them too. It returns how many users it changed;
// ErrNotFound for no such tariff.
func (s *Users) ApplyTariffToAll(ctx context.Context, tariffID int64, parts TariffParts) (int, error) {
	n := 0
	// A statement for each part, over all the users at once. READ COMMITTED, the users locked
	// in id order first, as the traffic batches lock them: a batch waits for this and this
	// for a batch, where a serializable snapshot would have the long one fail and start over.
	err := s.st.TxRC(ctx, func(q *db.Queries) error {
		n = 0
		if _, err := q.GetTariff(ctx, tariffID); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		ids, err := q.ListUserIDsOfTariff(ctx, sql.NullInt64{Int64: tariffID, Valid: true})
		if err != nil || len(ids) == 0 {
			return err
		}
		locked, err := q.LockUsers(ctx, ids)
		if err != nil {
			return err
		}
		n = len(locked)
		if parts.Traffic || parts.Devices || parts.Speed {
			if err := q.ApplyTariffLimits(ctx, db.ApplyTariffLimitsParams{Traffic: parts.Traffic, Devices: parts.Devices, Speed: parts.Speed,
				Now: s.now().Unix(), TariffID: tariffID}); err != nil {
				return err
			}
		}
		if parts.Groups {
			if err := q.ApplyTariffUsersPools(ctx, tariffID); err != nil {
				return err
			}
			// What they bought for a group the tariff now has otherwise goes to their main traffic.
			return q.MoveTariffGroupGrantsToMain(ctx, sql.NullInt64{Int64: tariffID, Valid: true})
		}
		return nil
	})
	if err == nil && n > 0 {
		s.changes.PoliciesChanged() // the nodes hold the users to the tariff's values
	}
	return n, err
}

// SetGroupInbounds makes ids the group's inbounds: they leave the group they were in, and
// the group's inbounds the list leaves out go back to the main servers.
func SetGroupInbounds(ctx context.Context, q *db.Queries, poolID int64, ids []int64) error {
	if _, err := q.GetTrafficPool(ctx, poolID); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if ids == nil {
		ids = []int64{}
	}
	_, err := q.SetGroupInbounds(ctx, db.SetGroupInboundsParams{Ids: ids, PoolID: poolID})
	return err
}

// PoolExhausted says whether the user's group with a limit of its own has no traffic left:
// its base quota is used and nothing is left of its grants (GrantsLeft.Pool). A group in
// any other mode never runs out on its own.
func PoolExhausted(p db.UserPool, grants int64) bool {
	return p.Mode == PoolOwn && model.TrafficLeft(p.TrafficLimit, p.UsedUp+p.UsedDown, grants) == 0
}

// ExhaustedPools are the user's groups that do not serve: left out of the subscription, or
// with a limit of their own run out, by group id. left is UserGrantsLeft of the user, which
// the caller has read.
func ExhaustedPools(ctx context.Context, q *db.Queries, userID int64, left GrantsLeft) (map[int64]bool, error) {
	ps, err := q.ListUserPools(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, p := range ps {
		if p.Mode == PoolClosed || PoolExhausted(p, left.Pool(userID, p.PoolID)) {
			out[p.PoolID] = true
		}
	}
	return out, nil
}
