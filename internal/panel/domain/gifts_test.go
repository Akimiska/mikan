package domain

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A tariff's gift adds to a quota the tariff gives: the main traffic with a limit, or a
// group with a limit of its own. A gift that does not is refused on the tariff, an archived
// package is no gift, and a payment gives each gift that still fits the subscription.
func TestTariffGifts(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, _ := setup(t, &now)
	ctx := context.Background()
	q := st.Q
	tariffs, _ := q.ListTariffs(ctx)
	tr := tariffs[1]
	if _, err := st.DB.ExecContext(ctx, "UPDATE tariffs SET traffic_limit = $1 WHERE id = $2", int64(100)<<30, tr.ID); err != nil {
		t.Fatal(err)
	}
	tr, _ = q.GetTariff(ctx, tr.ID)
	pool, err := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	pk := NewPackages(st, func() time.Time { return now })
	mainPack, err := pk.Create(ctx, PackageInput{Name: "+10", Bytes: 10 * GiB, Lifetime: LifetimeUsed})
	if err != nil {
		t.Fatal(err)
	}
	wlPack, err := pk.Create(ctx, PackageInput{Name: "+5 WL", Bytes: 5 * GiB, PoolID: pool.ID, Lifetime: LifetimePeriod})
	if err != nil {
		t.Fatal(err)
	}
	set := func(ids ...int64) error {
		return st.Tx(ctx, func(q *db.Queries) error { return SetTariffGifts(ctx, q, tr, ids) })
	}
	gifts := func() int {
		rows, _ := q.ListTariffGifts(ctx, tr.ID)
		return len(rows)
	}

	// The group is in the main traffic: its package adds to no quota the tariff gives.
	var fe *FieldError
	if err := set(mainPack.ID, wlPack.ID); !errors.As(err, &fe) || fe.Code != "gift_not_fit" {
		t.Fatalf("a gift for a group in the main traffic: %v", err)
	}
	if err := q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: tr.ID, PoolID: pool.ID, Mode: PoolOwn, TrafficLimit: sql.NullInt64{Int64: 20 * GiB, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := set(mainPack.ID, wlPack.ID, wlPack.ID); err != nil || gifts() != 2 {
		t.Fatalf("gifts that fit: %v, %d", err, gifts())
	}

	// Bought: the subscription has the tariff's group, each gift comes as a grant of it.
	u, err := users.Create(ctx, CreateInput{Name: "a", TariffID: tr.ID})
	if err != nil {
		t.Fatal(err)
	}
	var gs []db.TrafficGrant
	if err := st.Tx(ctx, func(q *db.Queries) error {
		var err error
		gs, err = GrantGifts(ctx, q, u, tr.ID, 0, now)
		return err
	}); err != nil || len(gs) != 2 {
		t.Fatalf("gifts granted: %+v %v", gs, err)
	}
	for _, g := range gs {
		if g.Source != SourceGift || (g.PackageID.Int64 == wlPack.ID) != (g.PoolID.Int64 == pool.ID) {
			t.Fatalf("a gift's grant: %+v", g)
		}
	}

	// The group is out of its own limit for this subscription: its gift is left out.
	if err := q.SetUserPool(ctx, db.SetUserPoolParams{UserID: u.ID, PoolID: pool.ID, Mode: PoolUnlimited}); err != nil {
		t.Fatal(err)
	}
	if err := st.Tx(ctx, func(q *db.Queries) error {
		var err error
		gs, err = GrantGifts(ctx, q, u, tr.ID, 0, now)
		return err
	}); err != nil || len(gs) != 1 || gs[0].PackageID.Int64 != mainPack.ID {
		t.Fatalf("only the gift that still fits: %+v %v", gs, err)
	}

	// An archived package leaves the tariffs it was a gift of, and is no gift any more.
	if err := pk.Archive(ctx, mainPack.ID); err != nil {
		t.Fatal(err)
	}
	if gifts() != 1 {
		t.Fatalf("an archived package is still a gift: %d", gifts())
	}
	if err := set(mainPack.ID); !errors.As(err, &fe) || fe.Code != "package_archived" {
		t.Fatalf("an archived gift: %v", err)
	}
}

// A package sold to some tariffs only is offered to their subscriptions alone.
func TestPackageForSomeTariffs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, users, _ := setup(t, &now)
	ctx := context.Background()
	q := st.Q
	tariffs, _ := q.ListTariffs(ctx)
	limit := 100 * GiB
	a, _ := users.Create(ctx, CreateInput{Name: "a", TariffID: tariffs[1].ID})
	b, _ := users.Create(ctx, CreateInput{Name: "b", TariffID: tariffs[2].ID})
	for _, u := range []db.User{a, b} {
		if _, err := users.Update(ctx, u.ID, Patch{TrafficLimit: &limit}); err != nil {
			t.Fatal(err)
		}
	}
	pk := NewPackages(st, func() time.Time { return now })
	p, err := pk.Create(ctx, PackageInput{Name: "+10", Bytes: 10 * GiB, Lifetime: LifetimeUsed, PriceStars: sql.NullInt64{Int64: 10, Valid: true}, OnSale: true,
		Tariffs: []int64{tariffs[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	offered := func(u db.User) bool {
		ps, err := PackagesFor(ctx, q, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return len(ps) == 1 && ps[0].ID == p.ID
	}
	if !offered(a) || offered(b) {
		t.Fatal("a package for one tariff is offered to that tariff alone")
	}
	var fe *FieldError
	if _, err := pk.Update(ctx, p.ID, PackageInput{Name: "+10", Bytes: 10 * GiB, Lifetime: LifetimeUsed, Tariffs: []int64{9999}}); !errors.As(err, &fe) || fe.Code != "tariff_not_found" {
		t.Fatalf("an unknown tariff: %v", err)
	}
	if _, err := pk.Update(ctx, p.ID, PackageInput{Name: "+10", Bytes: 10 * GiB, Lifetime: LifetimeUsed, PriceStars: sql.NullInt64{Int64: 10, Valid: true}, OnSale: true}); err != nil {
		t.Fatal(err)
	}
	if !offered(a) || !offered(b) {
		t.Fatal("a package for every tariff is offered to both")
	}
}

// A group in the main traffic spends it; with a limit of its own it counts apart.
func TestMainGroupCountsToTheMainTraffic(t *testing.T) {
	e := newGrantsEnv(t, 100)
	p, err := e.st.Q.CreateTrafficPool(e.ctx, db.CreateTrafficPoolParams{Name: "EU", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	e.countPool(p.ID, 30) // no row: the main traffic
	u, _ := e.st.Q.GetUser(e.ctx, e.u.ID)
	if u.UsedUp != 30 {
		t.Fatalf("a group in the main traffic spends it: %d", u.UsedUp)
	}
	if err := e.st.Q.SetUserPool(e.ctx, db.SetUserPoolParams{UserID: e.u.ID, PoolID: p.ID, Mode: PoolOwn, TrafficLimit: sql.NullInt64{Int64: 50, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	e.countPool(p.ID, 5)
	u, _ = e.st.Q.GetUser(e.ctx, e.u.ID)
	up, _ := e.st.Q.GetUserPool(e.ctx, db.GetUserPoolParams{UserID: e.u.ID, PoolID: p.ID})
	if u.UsedUp != 30 || up.UsedUp != 5 || u.TotalUp != 35 {
		t.Fatalf("with its own limit: main %d, group %d, total %d", u.UsedUp, up.UsedUp, u.TotalUp)
	}
}
