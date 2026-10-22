package domain

import (
	"context"
	"time"

	"mikan/internal/panel/settings"
)

// The sums of each subscription's bytes by site, while the admin has the detail on
// (settings.TrafficDetail): kept by UTC day for DetailDays.
const (
	DefaultDetailDays = 7
	MaxDetailDays     = 90
	// MaxSitesPerSlot bounds what one batch may bring for one slot: a node is a server
	// somebody else may run.
	MaxSitesPerSlot = 300
)

// SiteDay is the UTC day t falls on, as user_sites keeps it.
func SiteDay(t time.Time) int64 { return t.UTC().Truncate(24 * time.Hour).Unix() }

// DetailDays is how many days the sums are kept.
func DetailDays(ctx context.Context, set *settings.Settings) int {
	days, ok, err := settings.Get[int](ctx, set, settings.KeyTrafficDetailDays)
	if err != nil || !ok || days < 1 || days > MaxDetailDays {
		return DefaultDetailDays
	}
	return days
}
