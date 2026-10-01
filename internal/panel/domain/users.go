// Package domain holds the business rules for users, tariffs and the slot pool.
package domain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

const (
	StateActive   = "active"
	StateExpiring = "expiring"
	StateLimited  = "limited"
	StateExpired  = "expired"
	StateDisabled = "disabled"

	expiringWindow = 7 * 24 * time.Hour
	day            = int64(24 * time.Hour / time.Second)
)

var (
	ErrNotFound = errors.New("not found")
	ErrNoSlots  = errors.New("no free slots")
)

// State derives what the user can do right now; limited and expired are never stored.
func State(u db.User, now time.Time) string {
	switch {
	case u.Status == "disabled":
		return StateDisabled
	case u.ExpiresAt.Valid && now.Unix() >= u.ExpiresAt.Int64:
		return StateExpired
	case u.TrafficLimit.Valid && u.UsedUp+u.UsedDown >= u.TrafficLimit.Int64:
		return StateLimited
	case u.ExpiresAt.Valid && time.Unix(u.ExpiresAt.Int64, 0).Sub(now) <= expiringWindow:
		return StateExpiring
	}
	return StateActive
}

func CanConnect(state string) bool { return state == StateActive || state == StateExpiring }

// NextReset is when the traffic counter of the current period drops to zero.
func NextReset(u db.User, now time.Time) (time.Time, bool) {
	switch u.ResetStrategy {
	case "month_start":
		// Monthly: on the billing day, or the 1st without one.
		return nextMonthPeriod(MonthPeriodStart(now, u.BillingDay), u.BillingDay), true
	case "period":
		return time.Unix(u.PeriodStart+max(u.PeriodDays, 1)*day, 0).UTC(), true
	}
	return time.Time{}, false
}

// Changes tells the node syncer what to push after a mutation.
type Changes interface {
	PoliciesChanged()
	SlotsChanged()
}

type Users struct {
	st      *store.Store
	now     func() time.Time
	pool    *Pool
	changes Changes
}

func NewUsers(st *store.Store, pool *Pool, changes Changes, now func() time.Time) *Users {
	return &Users{st: st, now: now, pool: pool, changes: changes}
}

type CreateInput struct {
	Name     string
	Contact  string
	Note     string
	Tags     []string
	TariffID int64
}

func (s *Users) Create(ctx context.Context, in CreateInput) (db.User, error) {
	u, err := s.create(ctx, in)
	if errors.Is(err, ErrNoSlots) {
		if err := s.pool.Refill(ctx, RefillBatch); err != nil {
			return db.User{}, err
		}
		s.changes.SlotsChanged()
		u, err = s.create(ctx, in)
	}
	if err != nil {
		return db.User{}, err
	}
	s.changes.PoliciesChanged()
	return u, nil
}

func (s *Users) create(ctx context.Context, in CreateInput) (db.User, error) {
	var u db.User
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		var err error
		u, err = s.createTx(ctx, q, in, false)
		return err
	})
	return u, err
}

// createTx makes a user on q's transaction; anyTariff also takes an archived tariff (one
// that was paid for before the admin archived it).
func (s *Users) createTx(ctx context.Context, q *db.Queries, in CreateInput, anyTariff bool) (db.User, error) {
	now := s.now().Unix()
	tags, err := encodeTags(in.Tags)
	if err != nil {
		return db.User{}, err
	}
	t, err := q.GetTariff(ctx, in.TariffID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && t.Archived != 0 && !anyTariff) {
		return db.User{}, fmt.Errorf("tariff %d: %w", in.TariffID, ErrNotFound)
	}
	if err != nil {
		return db.User{}, err
	}
	slot, err := q.TakeFreeSlot(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, ErrNoSlots
	}
	if err != nil {
		return db.User{}, err
	}
	u, err := q.CreateUser(ctx, db.CreateUserParams{
		Name: strings.TrimSpace(in.Name), Contact: strings.TrimSpace(in.Contact), Note: in.Note, Tags: tags,
		TariffID: sql.NullInt64{Int64: t.ID, Valid: true}, TrafficLimit: t.TrafficLimit, DeviceLimit: t.DeviceLimit,
		ResetStrategy: t.ResetStrategy, PeriodDays: 30, PeriodStart: now,
		ExpiresAt:  tariffExpiry(time.Unix(now, 0), durationTariff{t.DurationDays, t.BillingDay}),
		BillingDay: t.BillingDay, SubToken: secure.Token(24), SlotID: sql.NullInt64{Int64: slot.ID, Valid: true}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return u, err
	}
	return u, ApplyTariffPools(ctx, q, u.ID, t.ID)
}

// Purchase applies a paid tariff on q's transaction, so the payment and its effect commit
// together. userID 0 (or a user deleted since the invoice) makes a new subscription named
// name. A renewal takes the tariff's limits, adds its term after the current one (from
// now when that already ended) and turns the user on; resetTraffic also starts a new
// traffic period (the payment buys a full quota). ErrNoSlots: refill and run again.
// The caller calls Changed after the commit.
func (s *Users) Purchase(ctx context.Context, q *db.Queries, userID, tariffID int64, name string, resetTraffic bool) (u db.User, created bool, err error) {
	if userID != 0 {
		u, err = q.GetUser(ctx, userID)
	}
	if userID == 0 || errors.Is(err, sql.ErrNoRows) {
		u, err = s.createTx(ctx, q, CreateInput{Name: name, Note: "Telegram", TariffID: tariffID}, true)
		return u, err == nil, err
	}
	if err != nil {
		return u, false, err
	}
	t, err := q.GetTariff(ctx, tariffID)
	if err != nil {
		return u, false, err
	}
	now := s.now()
	base := now
	if u.ExpiresAt.Valid && time.Unix(u.ExpiresAt.Int64, 0).After(now) {
		base = time.Unix(u.ExpiresAt.Int64, 0)
	}
	billingDay := t.BillingDay
	if !billingDay.Valid {
		billingDay = u.BillingDay
	}
	u, err = q.UpdateUser(ctx, db.UpdateUserParams{
		Name: u.Name, Contact: u.Contact, Note: u.Note, Tags: u.Tags, Status: "active", TariffID: sql.NullInt64{Int64: t.ID, Valid: true},
		TrafficLimit: t.TrafficLimit, DeviceLimit: t.DeviceLimit, ResetStrategy: t.ResetStrategy, PeriodDays: u.PeriodDays, PeriodStart: u.PeriodStart,
		ExpiresAt: tariffExpiry(base, durationTariff{t.DurationDays, billingDay}), Inbounds: u.Inbounds, BillingDay: billingDay,
		UpdatedAt: now.Unix(), ID: u.ID,
	})
	if err != nil {
		return u, false, err
	}
	if err := ApplyTariffPools(ctx, q, u.ID, t.ID); err != nil {
		return u, false, err
	}
	if !resetTraffic {
		return u, false, nil
	}
	if err := q.ResetUserPools(ctx, u.ID); err != nil {
		return u, false, err
	}
	if err := q.ResetUserTraffic(ctx, db.ResetUserTrafficParams{PeriodStart: now.Unix(), UpdatedAt: now.Unix(), ID: u.ID}); err != nil {
		return u, false, err
	}
	u, err = q.GetUser(ctx, u.ID)
	return u, false, err
}

// RefillSlots tops the slot pool up after ErrNoSlots.
func (s *Users) RefillSlots(ctx context.Context) error {
	if err := s.pool.Refill(ctx, RefillBatch); err != nil {
		return err
	}
	s.changes.SlotsChanged()
	return nil
}

// Changed tells the nodes about users changed on a transaction of the caller's.
func (s *Users) Changed() { s.changes.PoliciesChanged() }

func expiry(from, days int64) sql.NullInt64 {
	if days <= 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: from + days*day, Valid: true}
}

func (s *Users) Get(ctx context.Context, id int64) (db.User, error) {
	u, err := s.st.Q.GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// Patch is a partial update; nil fields are left unchanged. ClearX removes a limit.
type Patch struct {
	Name, Contact, Note *string
	Tags                *[]string
	Disabled            *bool
	TrafficLimit        *int64
	ClearTrafficLimit   bool
	DeviceLimit         *int64
	ClearDeviceLimit    bool
	ExpiresAt           *time.Time
	ClearExpiry         bool
	BillingDay          *int64 // 1–31: terms end on that day of the month
	ClearBillingDay     bool
	Inbounds            *[]int64 // empty slice = all inbounds
	TariffID            *int64   // applies the tariff's limits and restarts the term from now
}

// ErrBadBillingDay: a billing day is 1–31.
var ErrBadBillingDay = errors.New("bad_billing_day")

func (s *Users) Update(ctx context.Context, id int64, p Patch) (db.User, error) {
	var out db.User
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		u, err := q.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := s.now().Unix()
		par := db.UpdateUserParams{
			Name: u.Name, Contact: u.Contact, Note: u.Note, Tags: u.Tags, Status: u.Status, TariffID: u.TariffID,
			TrafficLimit: u.TrafficLimit, DeviceLimit: u.DeviceLimit, ResetStrategy: u.ResetStrategy,
			PeriodDays: u.PeriodDays, PeriodStart: u.PeriodStart, ExpiresAt: u.ExpiresAt, Inbounds: u.Inbounds,
			BillingDay: u.BillingDay, UpdatedAt: now, ID: u.ID,
		}
		if p.TariffID != nil {
			t, err := q.GetTariff(ctx, *p.TariffID)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && t.Archived != 0) {
				return fmt.Errorf("tariff %d: %w", *p.TariffID, ErrNotFound)
			}
			if err != nil {
				return err
			}
			par.TariffID = sql.NullInt64{Int64: t.ID, Valid: true}
			par.TrafficLimit, par.DeviceLimit, par.ResetStrategy, par.BillingDay = t.TrafficLimit, t.DeviceLimit, t.ResetStrategy, t.BillingDay
			par.ExpiresAt = tariffExpiry(time.Unix(now, 0), durationTariff{t.DurationDays, t.BillingDay})
			if err := ApplyTariffPools(ctx, q, u.ID, t.ID); err != nil {
				return err
			}
		}
		switch {
		case p.ClearBillingDay:
			par.BillingDay = sql.NullInt64{}
		case p.BillingDay != nil:
			if !ValidBillingDay(*p.BillingDay) {
				return ErrBadBillingDay
			}
			par.BillingDay = sql.NullInt64{Int64: *p.BillingDay, Valid: true}
		}
		if p.Name != nil {
			par.Name = strings.TrimSpace(*p.Name)
		}
		if p.Contact != nil {
			par.Contact = strings.TrimSpace(*p.Contact)
		}
		if p.Note != nil {
			par.Note = *p.Note
		}
		if p.Tags != nil {
			if par.Tags, err = encodeTags(*p.Tags); err != nil {
				return err
			}
		}
		if p.Disabled != nil {
			par.Status = "active"
			if *p.Disabled {
				par.Status = "disabled"
			}
		}
		switch {
		case p.ClearTrafficLimit:
			par.TrafficLimit = sql.NullInt64{}
		case p.TrafficLimit != nil:
			par.TrafficLimit = sql.NullInt64{Int64: *p.TrafficLimit, Valid: true}
		}
		switch {
		case p.ClearDeviceLimit:
			par.DeviceLimit = sql.NullInt64{}
		case p.DeviceLimit != nil:
			par.DeviceLimit = sql.NullInt64{Int64: *p.DeviceLimit, Valid: true}
		}
		switch {
		case p.ClearExpiry:
			par.ExpiresAt = sql.NullInt64{}
		case p.ExpiresAt != nil:
			par.ExpiresAt = sql.NullInt64{Int64: p.ExpiresAt.Unix(), Valid: true}
		}
		if p.Inbounds != nil {
			if len(*p.Inbounds) == 0 {
				par.Inbounds = sql.NullString{}
			} else {
				raw, _ := json.Marshal(*p.Inbounds)
				par.Inbounds = sql.NullString{String: string(raw), Valid: true}
			}
		}
		out, err = q.UpdateUser(ctx, par)
		return err
	})
	if err == nil {
		s.changes.PoliciesChanged()
	}
	return out, err
}

// Extend adds days to the current expiry, or to now if the term already ended.
func (s *Users) Extend(ctx context.Context, id int64, days int64) (db.User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return u, err
	}
	now := s.now()
	base := now
	if u.ExpiresAt.Valid && time.Unix(u.ExpiresAt.Int64, 0).After(now) {
		base = time.Unix(u.ExpiresAt.Int64, 0)
	}
	until := base.Add(time.Duration(days) * 24 * time.Hour)
	enable := false
	return s.Update(ctx, id, Patch{ExpiresAt: &until, Disabled: &enable})
}

// ExtendMonths adds n months: to the n-th billing day, or without one to the same day of
// the month (see AddMonths). A term that already ended restarts from now.
func (s *Users) ExtendMonths(ctx context.Context, id int64, n int) (db.User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return u, err
	}
	now := s.now()
	base := now
	if u.ExpiresAt.Valid && time.Unix(u.ExpiresAt.Int64, 0).After(now) {
		base = time.Unix(u.ExpiresAt.Int64, 0)
	}
	until := AddMonths(base, n, u.BillingDay)
	enable := false
	return s.Update(ctx, id, Patch{ExpiresAt: &until, Disabled: &enable})
}

// ExtendPeriod adds one paid period: a month up to the billing day, or 30 days without one.
func (s *Users) ExtendPeriod(ctx context.Context, id int64) (db.User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return u, err
	}
	if u.BillingDay.Valid {
		return s.ExtendMonths(ctx, id, 1)
	}
	return s.Extend(ctx, id, 30)
}

func (s *Users) ResetTraffic(ctx context.Context, id int64) (db.User, error) {
	now := s.now().Unix()
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		if err := q.ResetUserTraffic(ctx, db.ResetUserTrafficParams{PeriodStart: now, UpdatedAt: now, ID: id}); err != nil {
			return err
		}
		return q.ResetUserPools(ctx, id)
	})
	if err != nil {
		return db.User{}, err
	}
	s.changes.PoliciesChanged()
	return s.Get(ctx, id)
}

// Reissue gives the user a new slot and subscription token. The old credentials stop
// working at once: the old slot is burned and stays denied until it is purged.
func (s *Users) Reissue(ctx context.Context, id int64) (db.User, error) {
	err := s.reissue(ctx, id)
	if errors.Is(err, ErrNoSlots) {
		if err := s.pool.Refill(ctx, RefillBatch); err != nil {
			return db.User{}, err
		}
		s.changes.SlotsChanged()
		err = s.reissue(ctx, id)
	}
	if err != nil {
		return db.User{}, err
	}
	s.changes.PoliciesChanged()
	return s.Get(ctx, id)
}

func (s *Users) reissue(ctx context.Context, id int64) error {
	now := s.now().Unix()
	return s.st.Tx(ctx, func(q *db.Queries) error {
		u, err := q.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		slot, err := q.TakeFreeSlot(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSlots
		}
		if err != nil {
			return err
		}
		if u.SlotID.Valid {
			if err := q.BurnSlot(ctx, db.BurnSlotParams{BurnedAt: sql.NullInt64{Int64: now, Valid: true}, ID: u.SlotID.Int64}); err != nil {
				return err
			}
		}
		// A new link: every bound device registers again with it.
		if err := burnDevices(ctx, q, id, now); err != nil {
			return err
		}
		return q.SetUserCredentials(ctx, db.SetUserCredentialsParams{SlotID: sql.NullInt64{Int64: slot.ID, Valid: true}, SubToken: secure.Token(24), UpdatedAt: now, ID: id})
	})
}

func (s *Users) Delete(ctx context.Context, id int64) error {
	now := s.now().Unix()
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		u, err := q.GetUser(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if u.SlotID.Valid {
			if err := q.BurnSlot(ctx, db.BurnSlotParams{BurnedAt: sql.NullInt64{Int64: now, Valid: true}, ID: u.SlotID.Int64}); err != nil {
				return err
			}
		}
		if err := burnDevices(ctx, q, id, now); err != nil {
			return err
		}
		return q.DeleteUser(ctx, id)
	})
	if err == nil {
		s.changes.PoliciesChanged()
	}
	return err
}

func encodeTags(tags []string) (string, error) {
	clean := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	raw, err := json.Marshal(clean)
	return string(raw), err
}

func DecodeTags(raw string) []string {
	var t []string
	_ = json.Unmarshal([]byte(raw), &t)
	if t == nil {
		t = []string{}
	}
	return t
}

func DecodeInbounds(raw sql.NullString) []int64 {
	if !raw.Valid {
		return nil
	}
	var ids []int64
	_ = json.Unmarshal([]byte(raw.String), &ids)
	return ids
}
