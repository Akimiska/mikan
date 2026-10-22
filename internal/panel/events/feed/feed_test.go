package feed_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/events/feed"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

// An event written on a transaction that rolls back is never told, and one written twice
// under the same key is told once.
func TestEmitIsPartOfTheTransaction(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Unix(1_800_000_000, 0)
	count := func() int {
		evs, err := st.Q.ListAddonEvents(ctx, db.ListAddonEventsParams{Types: []string{}, Lim: 100})
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}
	boom := errors.New("boom")
	err = st.Tx(ctx, func(q *db.Queries) error {
		if err := feed.Emit(ctx, q, now, events.UserCreated, 1, 0, events.User{Source: "admin"}, "user.created:1"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || count() != 0 {
		t.Fatalf("a rolled back transaction told %d events (%v)", count(), err)
	}
	// The key of the rolled back one is free: the retry tells it.
	for range 2 {
		if err := st.Tx(ctx, func(q *db.Queries) error {
			return feed.Emit(ctx, q, now, events.UserCreated, 1, 0, events.User{Source: "admin"}, "user.created:1")
		}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 1 {
		t.Fatalf("%d events after a retry", count())
	}
}
