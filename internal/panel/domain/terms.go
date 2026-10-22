package domain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"mikan/internal/panel/store/db"
)

// What a term may cost. Traffic packages and the trial are sold on the same scale, so the
// API, the admin pages and these checks all mean the same numbers.
const (
	MinPriceStars = 1
	MaxPriceStars = 10_000
	MinPriceRub   = 100 // kopecks: a ruble
	MaxPriceRub   = 100_000_000
)

// PriceStarsOK, PriceRubOK: n is a price the panel sells at. No price at all (0: not sold
// in that currency) is not one, and whether it is allowed is the caller's.
func PriceStarsOK(n int64) bool { return n >= MinPriceStars && n <= MaxPriceStars }
func PriceRubOK(n int64) bool   { return n >= MinPriceRub && n <= MaxPriceRub }

// CheckPrices: a pair of prices as a tariff's; an unset price is not sold in that currency
// and passes. The error names the price that is out of range.
func CheckPrices(stars, rub sql.NullInt64) error {
	switch {
	case stars.Valid && !PriceStarsOK(stars.Int64):
		return fieldErr("price_stars", "bad_price")
	case rub.Valid && !PriceRubOK(rub.Int64):
		return fieldErr("price_rub", "bad_price")
	}
	return nil
}

// Term is one way a tariff is sold: for so many days (0: no end date; with a billing day,
// months of 30 days each) at these prices.
type Term struct {
	Days       int64
	PriceStars sql.NullInt64 // not for Stars when invalid
	PriceRub   sql.NullInt64 // kopecks; not for rubles when invalid
}

// TariffTerms are the terms tariff t is sold for, in order: its rows in tariff_terms, or,
// without any, the one term in the tariff itself. The first is the tariff's default term.
func TariffTerms(t db.Tariff, rows []db.TariffTerm) []Term {
	var out []Term
	for _, r := range rows {
		if r.TariffID == t.ID {
			out = append(out, Term{Days: r.Days, PriceStars: r.PriceStars, PriceRub: r.PriceRub})
		}
	}
	if len(out) == 0 {
		out = []Term{{Days: t.DurationDays, PriceStars: t.PriceStars, PriceRub: t.PriceRub}}
	}
	return out
}

// TermsOf reads the terms tariff t is sold for.
func TermsOf(ctx context.Context, q *db.Queries, t db.Tariff) ([]Term, error) {
	rows, err := q.ListTariffTerms(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return TariffTerms(t, rows), nil
}

// FindTerm is the term of so many days among terms.
func FindTerm(terms []Term, days int64) (Term, bool) {
	for _, t := range terms {
		if t.Days == days {
			return t, true
		}
	}
	return Term{}, false
}

// SetTariffTerms writes the terms of a tariff whose own columns already hold the first
// one: one term needs no rows, two or more are stored in order.
func SetTariffTerms(ctx context.Context, q *db.Queries, tariffID int64, terms []Term) error {
	if err := q.DeleteTariffTerms(ctx, tariffID); err != nil {
		return err
	}
	if len(terms) < 2 {
		return nil
	}
	for i, t := range terms {
		if err := q.AddTariffTerm(ctx, db.AddTariffTermParams{TariffID: tariffID, Days: t.Days, PriceStars: t.PriceStars, PriceRub: t.PriceRub, Sort: int64(i)}); err != nil {
			return err
		}
	}
	return nil
}

// termDays is the term bought on tariff t: the payment's, or the tariff's own.
func termDays(t db.Tariff, term sql.NullInt64) int64 {
	if term.Valid {
		return term.Int64
	}
	return t.DurationDays
}

// carryOver is what is left of a term (left) when a renewal moves the subscription from
// tariff from to tariff to, bought for days: the days left are worth what a day costs on
// the old tariff, and buy as many days of the new one as that pays for at a day's price of
// the term bought. A day is priced in rubles when both tariffs are sold for rubles, else in
// Stars when both are. The old tariff's day is the one of its term as long as the term
// bought, else of its first. With no price the two share (a tariff the admin gives and does
// not sell), or the old tariff gone, the days stay as they are. Without this, a year of the cheapest tariff and a month of the dearest
// would be a year and a month of the dearest (decided 2026-10-21).
func carryOver(ctx context.Context, q *db.Queries, from int64, to db.Tariff, days int64, left time.Duration) (time.Duration, error) {
	old, err := q.GetTariff(ctx, from)
	if errors.Is(err, sql.ErrNoRows) {
		return left, nil
	}
	if err != nil {
		return 0, err
	}
	oldTerms, err := TermsOf(ctx, q, old)
	if err != nil {
		return 0, err
	}
	newTerms, err := TermsOf(ctx, q, to)
	if err != nil {
		return 0, err
	}
	bought, ok := FindTerm(newTerms, days)
	if !ok {
		bought = newTerms[0]
	}
	was, ok := FindTerm(oldTerms, days)
	if !ok {
		was = oldTerms[0]
	}
	oldDay, newDay, ok := dayPrices(was, bought)
	if !ok || newDay <= 0 {
		return left, nil
	}
	return time.Duration(float64(left) * oldDay / newDay), nil
}

// dayPrices are what a day of a and of b costs, in the currency both are sold for: rubles
// first, then Stars. false: none they share.
func dayPrices(a, b Term) (float64, float64, bool) {
	if a.Days <= 0 || b.Days <= 0 {
		return 0, 0, false
	}
	day := func(price sql.NullInt64, days int64) float64 { return float64(price.Int64) / float64(days) }
	switch {
	case a.PriceRub.Valid && b.PriceRub.Valid:
		return day(a.PriceRub, a.Days), day(b.PriceRub, b.Days), true
	case a.PriceStars.Valid && b.PriceStars.Valid:
		return day(a.PriceStars, a.Days), day(b.PriceStars, b.Days), true
	}
	return 0, 0, false
}

// What a tariff's description may hold: it goes under the tariff's name in the bot and in
// the Mini App, so a screenful at most.
const (
	MaxDescriptionRunes = 300
	MaxDescriptionLines = 5
)

// CleanDescription is the admin's text for buyers as it is stored: line breaks as \n, the
// ends trimmed. A text over MaxDescriptionRunes characters or MaxDescriptionLines lines is
// refused, and the error names the field as the API does; empty is allowed (nothing to add).
func CleanDescription(s string) (string, error) {
	s = strings.TrimSpace(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s))
	if utf8.RuneCountInString(s) > MaxDescriptionRunes {
		return "", fieldErr("description", "description_too_long")
	}
	if s != "" && strings.Count(s, "\n")+1 > MaxDescriptionLines {
		return "", fieldErr("description", "description_too_many_lines")
	}
	return s, nil
}
