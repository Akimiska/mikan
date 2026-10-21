package domain

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A tariff changed after it was given out reaches its users when the admin says so, in the
// parts the admin picks: the tariff's values replace their own, what they used and their
// terms stay, other tariffs' users are left alone.
func TestApplyTariffToAll(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, ch := setup(t, &now)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	tr, other := tariffs[0], tariffs[1]
	a, _ := users.Create(ctx, CreateInput{Name: "a", TariffID: tr.ID})
	b, _ := users.Create(ctx, CreateInput{Name: "b", TariffID: other.ID})
	var pool int64
	if err := st.DB.QueryRowContext(ctx, "INSERT INTO traffic_pools(name,created_at) VALUES('WL',1) RETURNING id").Scan(&pool); err != nil {
		t.Fatal(err)
	}
	// Given out before the tariff took the group with a limit: it is in the main traffic.
	if _, err := st.DB.ExecContext(ctx, "INSERT INTO user_pools(user_id,pool_id,used_up) VALUES($1,$2,7)", a.ID, pool); err != nil {
		t.Fatal(err)
	}
	if err := st.Q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: tr.ID, PoolID: pool, Mode: PoolOwn, TrafficLimit: sql.NullInt64{Int64: 100 << 30, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	// The tariff's own values change too, and so did the user's.
	if _, err := st.DB.ExecContext(ctx, "UPDATE tariffs SET traffic_limit = $1, speed_limit = 25, device_limit = 4 WHERE id = $2", int64(300)<<30, tr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, "UPDATE users SET speed_limit = 5, extra_devices = 1 WHERE id = $1", a.ID); err != nil {
		t.Fatal(err)
	}

	// Only the groups: the traffic and the speed the user had stay.
	n, err := users.ApplyTariffToAll(ctx, tr.ID, TariffParts{Groups: true})
	if err != nil || n != 1 {
		t.Fatalf("applied to %d: %v", n, err)
	}
	ps, _ := st.Q.ListUserPools(ctx, a.ID)
	if len(ps) != 1 || ps[0].Mode != PoolOwn || ps[0].TrafficLimit.Int64 != 100<<30 || ps[0].UsedUp != 7 {
		t.Fatalf("a's groups: %+v", ps)
	}
	if u, _ := st.Q.GetUser(ctx, a.ID); u.SpeedLimit.Int64 != 5 || u.TrafficLimit.Int64 == 300<<30 {
		t.Fatalf("the groups alone changed more: %+v", u)
	}
	if ps, _ := st.Q.ListUserPools(ctx, b.ID); len(ps) != 0 {
		t.Fatalf("another tariff's user changed: %+v", ps)
	}
	if ch.policies == 0 {
		t.Fatal("the nodes are not told")
	}

	// The rest: traffic, speed and devices, the device bought on top kept on top.
	if _, err := users.ApplyTariffToAll(ctx, tr.ID, TariffParts{Traffic: true, Devices: true, Speed: true}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.Q.GetUser(ctx, a.ID)
	if u.TrafficLimit.Int64 != 300<<30 || u.SpeedLimit.Int64 != 25 || u.DeviceLimit.Int64 != 5 || u.ExpiresAt != a.ExpiresAt {
		t.Fatalf("a after the rest: traffic %d speed %d devices %d expires %v (was %v)", u.TrafficLimit.Int64, u.SpeedLimit.Int64, u.DeviceLimit.Int64, u.ExpiresAt, a.ExpiresAt)
	}
	if ub, _ := st.Q.GetUser(ctx, b.ID); ub.TrafficLimit == u.TrafficLimit {
		t.Fatal("another tariff's user took the tariff's traffic")
	}

	if _, err := users.ApplyTariffToAll(ctx, 9999, TariffParts{Groups: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no such tariff: %v", err)
	}
}
