package billing

import (
	"context"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// The tariff's gift comes with every payment for it, a grant of that payment: the
// purchase gives it, a renewal gives it again, and a refund takes back the one of the
// payment refunded.
func TestTariffGiftsComeWithThePayment(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := e.st.Q
	_, err := e.st.DB.ExecContext(ctx, "UPDATE tariffs SET traffic_limit = $1 WHERE id = $2", int64(100)<<30, e.sale.ID)
	must(t, err)
	sale, err := q.GetTariff(ctx, e.sale.ID)
	must(t, err)
	pk := domain.NewPackages(e.st, func() time.Time { return e.now })
	gift, err := pk.Create(ctx, domain.PackageInput{Name: "bonus", Bytes: 20 * domain.GiB, Lifetime: domain.LifetimeUsed})
	must(t, err)
	must(t, e.st.Tx(ctx, func(q *db.Queries) error { return domain.SetTariffGifts(ctx, q, sale, []int64{gift.ID}) }))

	p := e.buy(0, "ch-gift")
	gs, err := q.ListUserGrants(ctx, p.UserID.Int64)
	must(t, err)
	if len(gs) != 1 || gs[0].Source != domain.SourceGift || gs[0].PaymentID.Int64 != p.ID || gs[0].Bytes != 20*domain.GiB {
		t.Fatalf("the purchase's gift: %+v", gs)
	}

	p2 := e.buy(p.UserID.Int64, "ch-gift-2")
	if gs, _ = q.ListUserGrants(ctx, p.UserID.Int64); len(gs) != 2 {
		t.Fatalf("a renewal gives the gift again: %+v", gs)
	}
	_, err = e.s.Refund(ctx, p2.ID)
	must(t, err)
	if gs, _ = q.ListUserGrants(ctx, p.UserID.Int64); len(gs) != 1 || gs[0].PaymentID.Int64 != p.ID {
		t.Fatalf("a refund takes back its payment's gift alone: %+v", gs)
	}
}

// What a plan is made of, as the bot and the Mini App write it: each group with a
// server in its mode, then the gifts.
func TestDescribeContents(t *testing.T) {
	o := Offer{
		Groups: []OfferGroup{{Name: "EU", Mode: domain.PoolMain}, {Name: "WL", Mode: domain.PoolOwn, Limit: 50 * domain.GiB},
			{Name: "TG", Mode: domain.PoolUnlimited}, {Name: "Premium", Mode: domain.PoolClosed}},
		Gifts: []OfferGift{{Package: db.TrafficPackage{Bytes: 20 * domain.GiB, Lifetime: domain.LifetimeUsed}, Pool: "WL"}},
	}
	got := DescribeContents(o, "ru")
	want := []string{"EU: входит", "WL: 50 ГБ отдельно", "TG: без лимита", "Premium: не входит", "В подарок: 20 ГБ · WL · пока не израсходован"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: %q, want %q", i, got[i], want[i])
		}
	}
	if en := DescribeContents(o, "en"); en[1] != "WL: 50 GB of its own" || en[4] != "Gift: 20 GB · WL · until used up" {
		t.Fatalf("en: %q", en)
	}
	if len(DescribeContents(Offer{}, "ru")) != 0 {
		t.Fatal("a plan without groups or gifts says nothing more")
	}
}
