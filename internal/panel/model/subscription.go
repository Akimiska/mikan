package model

import (
	"database/sql"
	"time"
)

// The states of a subscription (State); limited and expired are never stored.
const (
	StateActive   = "active"
	StateExpiring = "expiring"
	StateLimited  = "limited"
	StateExpired  = "expired"
	StateDisabled = "disabled"
)

// ExpiringWindow is how long before its end a subscription counts as expiring.
const ExpiringWindow = 7 * 24 * time.Hour

// day is a day in seconds.
const day = int64(24 * time.Hour / time.Second)

// State derives what the user can do right now. grants is what is left of the user's active
// main grants (GrantsLeft.Main): a user past the base quota with grants left is not limited.
func State(u User, grants int64, now time.Time) string {
	switch {
	case u.Status == "disabled":
		return StateDisabled
	case u.ExpiresAt.Valid && now.Unix() >= u.ExpiresAt.Int64:
		return StateExpired
	case TrafficLeft(u.TrafficLimit, u.UsedUp+u.UsedDown, grants) == 0:
		return StateLimited
	case u.ExpiresAt.Valid && Time(u.ExpiresAt.Int64).Sub(now) <= ExpiringWindow:
		return StateExpiring
	}
	return StateActive
}

// TrafficLeft is what the user may still use of a quota: the base limit's rest plus the
// active grants. -1: unlimited (no limit), which never touches grants.
func TrafficLeft(limit sql.NullInt64, used, grants int64) int64 {
	if !limit.Valid {
		return -1
	}
	return max(0, limit.Int64-used) + max(0, grants)
}

// NextReset is when the traffic counter of the current period drops to zero.
func NextReset(u User, now time.Time) (time.Time, bool) {
	switch u.ResetStrategy {
	case "month_start":
		// Monthly: on the billing day, or the 1st without one.
		return ClampTime(NextMonthPeriod(MonthPeriodStart(now, u.BillingDay), u.BillingDay)), true
	case "period":
		return Time(u.PeriodStart + max(u.PeriodDays, 1)*day), true
	}
	return time.Time{}, false
}

// DayOfMonth is day d of a month at clock's time of day, or the month's last day when it is
// shorter.
func DayOfMonth(year int, month time.Month, d int, clock time.Time) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(d, last), clock.Hour(), clock.Minute(), clock.Second(), 0, time.UTC)
}

// MonthPeriodStart is when the current monthly traffic period began: the last billing day
// (the 1st without one) at or before now.
func MonthPeriodStart(now time.Time, billingDay sql.NullInt64) time.Time {
	d := 1
	if billingDay.Valid {
		d = int(billingDay.Int64)
	}
	now = now.UTC()
	start := DayOfMonth(now.Year(), now.Month(), d, time.Time{})
	if start.After(now) {
		start = DayOfMonth(now.Year(), now.Month()-1, d, time.Time{})
	}
	return start
}

// NextMonthPeriod is when the monthly traffic period that began at start ends.
func NextMonthPeriod(start time.Time, billingDay sql.NullInt64) time.Time {
	d := 1
	if billingDay.Valid {
		d = int(billingDay.Int64)
	}
	return DayOfMonth(start.Year(), start.Month()+1, d, time.Time{})
}

// TermMonths turns a tariff's duration into months for a billing day: 30 days is one.
func TermMonths(days int64) int {
	return max(1, int((days+15)/30))
}

// The bounds of a time the panel keeps: what JSON and the database can hold.
const (
	MinUnix int64 = -62167219200 // 0000-01-01T00:00:00Z
	MaxUnix int64 = 253402300799 // 9999-12-31T23:59:59Z
)

// Time is unix seconds as a UTC time clamped to [0000-01-01, 9999-12-31], so it always
// serializes.
func Time(sec int64) time.Time {
	if sec < MinUnix {
		return time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if sec > MaxUnix {
		return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}
	return time.Unix(sec, 0).UTC()
}

// ClampTime clamps t to [0000-01-01, 9999-12-31] so JSON serialization never fails.
func ClampTime(t time.Time) time.Time { return Time(t.Unix()) }

// The device modes of a subscription: what its device limit counts.
const (
	// DeviceModeBound: the limit is the number of bound devices.
	DeviceModeBound = "bound"
	// DeviceModeOnline: any number of devices, so many online at once.
	DeviceModeOnline = "online"
)

// BindsDevices says whether the device limit of a user with this mode is the number of
// bound devices; in the online mode it is not.
func BindsDevices(mode string) bool { return mode != DeviceModeOnline }

// How a tariff or a subscription has a server group.
const (
	PoolMain      = "main"      // in, its traffic counts to the main traffic
	PoolOwn       = "own"       // in, with a limit of its own apart from the main traffic
	PoolUnlimited = "unlimited" // in, without a limit, apart from the main traffic
	PoolClosed    = "closed"    // not in: left out of the subscription, the nodes turn the user away
)

// How long a traffic package lasts.
const (
	LifetimeUsed   = "used"   // until used up
	LifetimePeriod = "period" // until the end of the traffic period it was given in
	LifetimeDays   = "days"   // N days from when it was given
)

// GiB is a gigabyte, as traffic is counted.
const GiB = int64(1) << 30

// MaxLinks is how many subscriptions one Telegram account may hold.
const MaxLinks = 5

// CampaignPrefix starts the bot's /start code of an ad campaign's link.
const CampaignPrefix = "c_"

// GrantsLeft is what is left of a subscription's active traffic packages: Main of the main
// traffic, Pools of each server group with a limit of its own, by its id.
type GrantsLeft struct {
	Main  int64           `json:"main"`
	Pools map[int64]int64 `json:"pools,omitempty"`
}

// UnbindRules are the admin's rules of the subscriber's own unbinding of devices.
type UnbindRules struct {
	// Off: the subscriber unbinds nothing themselves, only the admin does.
	Off bool `json:"off,omitempty"`
	// Limit is how many devices the subscriber may unbind in Days; 0: as many as they like.
	Limit int `json:"limit"`
	// Days is the window the limit counts in, sliding: an unbind leaves it Days later.
	Days int `json:"days"`
	// ReturnHours is how long a device the subscriber unbound may not bind again; 0: at
	// once, ReturnNever: until the admin lets it.
	ReturnHours int `json:"return_hours"`
}

// ReturnNever: a device the subscriber unbound comes back only when the admin lets it.
const ReturnNever = -1

// UnbindState is what the subscriber may do now under the rules.
type UnbindState struct {
	Rules UnbindRules
	// Used is how many unbinds of theirs the window holds.
	Used int
	// Next is when they may unbind again; zero: now.
	Next time.Time
}

// Left is how many unbinds the window still has room for; -1 without a limit, 0 when the
// subscriber may not unbind at all.
func (s UnbindState) Left() int { return left(s.Rules.Off, s.Rules.Limit, s.Used) }

// ReissueRules are the admin's rules of the subscriber's own new link.
type ReissueRules struct {
	// Off: the subscriber reissues nothing themselves, only the admin does.
	Off bool `json:"off,omitempty"`
	// Limit is how many times the subscriber may reissue in Days; 0: as often as they like.
	Limit int `json:"limit"`
	// Days is the window the limit counts in, sliding: a reissue leaves it Days later.
	Days int `json:"days"`
}

// ReissueState is what the subscriber may do now under the rules.
type ReissueState struct {
	Rules ReissueRules
	// Used is how many reissues of theirs the window holds.
	Used int
	// Next is when they may reissue again; zero: now.
	Next time.Time
}

// Left is how many reissues the window still has room for; -1 without a limit, 0 when the
// subscriber may not reissue at all.
func (s ReissueState) Left() int { return left(s.Rules.Off, s.Rules.Limit, s.Used) }

// left is what a limit of so many in a window leaves after used: -1 without a limit, 0 when
// it is off.
func left(off bool, limit, used int) int {
	switch {
	case off:
		return 0
	case limit == 0:
		return -1
	}
	return max(0, limit-used)
}
