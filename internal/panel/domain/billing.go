package domain

import (
	"database/sql"
	"time"

	"mikan/internal/panel/model"
	"mikan/internal/panel/store"
)

// A billing day (1–31) ends every term on that day of the month: "+1 month" moves the
// expiry from the 10th to the next 10th, not by 30 days. A shorter month ends the term on
// its last day. All dates are UTC; a term keeps the time of day it started with.

// MinFirstTerm: a term that would end on the nearest billing day sooner than this runs to
// the billing day after it — the first payment covers at least half a month.
const MinFirstTerm = 15 * 24 * time.Hour

// NextBillingDate is the first day-d moment after t, at t's time of day.
func NextBillingDate(t time.Time, d int) time.Time {
	t = t.UTC()
	next := model.DayOfMonth(t.Year(), t.Month(), d, t)
	if !next.After(t) {
		next = model.DayOfMonth(t.Year(), t.Month()+1, d, t)
	}
	return next
}

// nextTerm is the billing day that ends a month started at base: the nearest one at least
// MinFirstTerm away.
func nextTerm(base time.Time, d int) time.Time {
	e := NextBillingDate(base, d)
	if e.Sub(base) < MinFirstTerm {
		e = NextBillingDate(e, d)
	}
	return e
}

// AddMonths extends a term by n months from base (the current expiry, or now when the
// term already ended): to the n-th term end on the billing day, or without one to the
// same day of the month n months later.
func AddMonths(base time.Time, n int, billingDay sql.NullInt64) time.Time {
	base = base.UTC()
	if !billingDay.Valid {
		return model.DayOfMonth(base.Year(), base.Month()+time.Month(n), base.Day(), base)
	}
	d := int(billingDay.Int64)
	e := nextTerm(base, d)
	for i := 1; i < n; i++ {
		e = NextBillingDate(e, d)
	}
	return e
}

// tariffExpiry is when a term on tariff t that starts now ends (invalid = never).
func tariffExpiry(now time.Time, t durationTariff) sql.NullInt64 {
	if t.days <= 0 {
		return sql.NullInt64{}
	}
	if t.billingDay.Valid {
		return sql.NullInt64{Int64: store.ClampUnix(AddMonths(now, model.TermMonths(t.days), t.billingDay).Unix()), Valid: true}
	}
	return expiry(now.Unix(), t.days)
}

type durationTariff struct {
	days       int64
	billingDay sql.NullInt64
}

// ValidBillingDay: 1–31.
func ValidBillingDay(d int64) bool { return d >= 1 && d <= 31 }
