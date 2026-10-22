// Package pace holds the waits the panel's workers and retry loops are made of: a call on a
// ticker, a sleep that ctx cuts short, and the pause that doubles with every failure.
package pace

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// Tick calls fn every d until ctx ends. The first call comes after d, not at once: a worker
// that must run first does that itself. d must be positive, and the ticks a slow fn misses
// are dropped, as with time.NewTicker.
func Tick(ctx context.Context, d time.Duration, fn func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn(ctx)
		}
	}
}

// Sleep waits d and says true; it says false, at once, when ctx ends first.
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Backoff is the pause between attempts that doubles with every failure in a row, from Base
// up to Max. A Max of zero or less leaves it uncapped.
type Backoff struct{ Base, Max time.Duration }

// Next is the pause for a failure that follows n others in a row: Base for the first, then
// twice that, and so on, never more than Max. An n of zero or less gives Base, whatever Max
// is.
func (b Backoff) Next(n int) time.Duration {
	limit := b.Max
	if limit <= 0 {
		limit = math.MaxInt64
	}
	d := b.Base
	for ; n > 0 && d > 0; n-- {
		if d > limit/2 {
			return limit
		}
		d *= 2
	}
	return d
}

// Jitter is a random time from zero up to, not including, d (zero for a d of zero or less).
// A pause of it keeps callers that failed together from coming back together.
func Jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return rand.N(d)
}
