package domain

import (
	"context"
	"database/sql"
	"errors"

	"mikan/internal/panel/store/db"
)

// Server groups (the traffic pools of GitHub issue #6): a group is a set of inbounds, and a
// tariff and a subscription hold each group in one mode. Users get the modes of their
// tariff, like its other limits, and the admin may change them per user. The inbounds in no
// group are the main servers: always in, on the main traffic.

// The modes of a group in a tariff or a subscription.
const (
	PoolMain      = "main"      // in, its traffic counts to the main traffic
	PoolOwn       = "own"       // in, with a limit of its own apart from the main traffic
	PoolUnlimited = "unlimited" // in, without a limit, apart from the main traffic
	PoolClosed    = "closed"    // not in: left out of the subscription, the nodes turn the user away
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
	return nil
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
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		n = 0
		t, err := q.GetTariff(ctx, tariffID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		ids, err := q.ListUserIDsOfTariff(ctx, sql.NullInt64{Int64: tariffID, Valid: true})
		if err != nil {
			return err
		}
		now := s.now().Unix()
		for _, id := range ids {
			u, err := q.GetUser(ctx, id)
			if err != nil {
				return err
			}
			if parts.Traffic || parts.Devices || parts.Speed {
				par := db.UpdateUserParams{
					Name: u.Name, Contact: u.Contact, Note: u.Note, Tags: u.Tags, Status: u.Status, TariffID: u.TariffID,
					TrafficLimit: u.TrafficLimit, DeviceLimit: u.DeviceLimit, SpeedLimit: u.SpeedLimit, ResetStrategy: u.ResetStrategy,
					PeriodDays: u.PeriodDays, PeriodStart: u.PeriodStart, ExpiresAt: u.ExpiresAt, Inbounds: u.Inbounds,
					BillingDay: u.BillingDay, UpdatedAt: now, ID: u.ID,
				}
				if parts.Traffic {
					par.TrafficLimit, par.ResetStrategy = t.TrafficLimit, t.ResetStrategy
				}
				if parts.Devices {
					// Devices bought on top of the tariff stay on top of it.
					par.DeviceLimit = t.DeviceLimit
					if par.DeviceLimit.Valid && u.ExtraDevices > 0 {
						par.DeviceLimit.Int64 += u.ExtraDevices
					}
				}
				if parts.Speed {
					par.SpeedLimit = t.SpeedLimit
				}
				if u, err = q.UpdateUser(ctx, par); err != nil {
					return err
				}
				if parts.Devices {
					if err := setDeviceMode(ctx, q, &u, t.DeviceMode, now); err != nil {
						return err
					}
				}
			}
			if parts.Groups {
				if err := ApplyTariffPools(ctx, q, id, tariffID); err != nil {
					return err
				}
			}
			n++
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
	return p.Mode == PoolOwn && TrafficLeft(p.TrafficLimit, p.UsedUp+p.UsedDown, grants) == 0
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
