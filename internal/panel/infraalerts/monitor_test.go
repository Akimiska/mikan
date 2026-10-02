package infraalerts

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"mikan/internal/panel/acme"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
)

func testMonitorStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ctx
}

func TestFreshMonitorStartsAfterExistingAutotuneEvents(t *testing.T) {
	st, ctx := testMonitorStore(t)
	conn, err := st.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO inbound_events(inbound_id,node_id,kind,network,old_value,new_value,reason,created_at) VALUES(1,1,'port','tcp','1','2','blocked',1)`); err != nil {
		t.Fatal(err)
	}
	m := New(st, settings.New(st.Q), nil, nil, nil, nil, nil, slog.Default(), time.Now)
	state, err := m.load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.AutoCursor != 1 {
		t.Fatalf("fresh cursor = %d, want 1", state.AutoCursor)
	}
}

func TestDisabledBotDoesNotKeepPendingAlerts(t *testing.T) {
	st, ctx := testMonitorStore(t)
	cfg := Default()
	cfg.AdminEnabled = true
	if err := settings.Set(ctx, settings.New(st.Q), KeyConfig, cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m := New(st, settings.New(st.Q), nil, nil, func() acme.Status { return acme.Status{Error: "broken", CheckedAt: now} }, nil, nil, slog.Default(), time.Now)
	m.round(ctx)
	state, err := m.load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Pending) != 0 {
		t.Fatalf("disabled bot retained %d pending alerts", len(state.Pending))
	}
}

func TestDeliveryQueueBoundsRetries(t *testing.T) {
	d := delivery{CreatedAt: 100}
	if !retryDelivery(&d, 100, context.DeadlineExceeded) || d.Attempts != 1 || d.NextAttemptAt != 105 {
		t.Fatalf("first retry = %+v", d)
	}
	d.Attempts = 7
	if retryDelivery(&d, 100, context.DeadlineExceeded) {
		t.Fatal("queue item exceeded the retry limit")
	}
}

func TestMergeDeliveryResultsPreservesNewItems(t *testing.T) {
	before := []delivery{{Key: "done", Target: "admin"}, {Key: "retry", Target: "admin"}}
	after := []delivery{{Key: "retry", Target: "admin", Attempts: 2, NextAttemptAt: 300}}
	latest := []delivery{{Key: "done", Target: "admin"}, {Key: "retry", Target: "admin", Text: "new text"}, {Key: "new", Target: "admin"}}
	got := mergeDeliveryResults(latest, before, after)
	if len(got) != 2 || got[0].Key != "retry" || got[0].Text != "new text" || got[0].Attempts != 2 || got[1].Key != "new" {
		t.Fatalf("merged queue = %+v", got)
	}
}
