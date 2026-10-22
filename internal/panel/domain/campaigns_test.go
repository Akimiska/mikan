package domain

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

// An account belongs to the first campaign it came by while it had no subscription; the
// campaign counts who came, bought and renewed, and what they paid.
func TestCampaigns(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(1_800_000_000, 0)
	c := NewCampaigns(st, func() time.Time { return now })
	vk, err := c.Create(ctx, "VK", "vk-oct")
	if err != nil {
		t.Fatal(err)
	}
	tg, err := c.Create(ctx, "TG", "")
	if err != nil || len(tg.Code) != 8 {
		t.Fatalf("a random code: %+v %v", tg, err)
	}
	if _, err := c.Create(ctx, "again", "VK-OCT"); !errors.Is(err, ErrCampaignTaken) {
		t.Fatalf("a taken code: %v", err)
	}
	if _, err := c.Create(ctx, "bad", "a b"); !errors.Is(err, ErrCampaignCode) {
		t.Fatalf("a bad code: %v", err)
	}

	tag := func(tgID int64, code string) bool {
		t.Helper()
		ok, err := c.Tag(ctx, tgID, code)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !tag(101, "vk-oct") || !tag(102, "vk-oct") || !tag(103, tg.Code) {
		t.Fatal("new accounts are tagged")
	}
	if tag(101, tg.Code) {
		t.Fatal("the first campaign stays the account's")
	}
	if tag(104, "nope") {
		t.Fatal("an unknown code tags nothing")
	}
	// A customer who taps an ad is not the ad's.
	u, err := st.Q.CreateUser(ctx, db.CreateUserParams{Name: "old", SubToken: "tok-old", PeriodStart: 1, CreatedAt: 1, UpdatedAt: 1, ResetStrategy: "none", Tags: "[]", Source: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 105, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if tag(105, "vk-oct") {
		t.Fatal("an account with a subscription is tagged")
	}

	tariff, err := st.Q.CreateTariff(ctx, db.CreateTariffParams{Name: "t", DurationDays: 30, ResetStrategy: "none", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	pay := func(tgID int64, kind string, amount int64) {
		t.Helper()
		p, err := st.Q.CreatePayment(ctx, db.CreatePaymentParams{Provider: "stars", Payload: kind + string(rune('a'+tgID%26)) + time.Now().String(), TgID: tgID, Kind: kind,
			UserID: sql.NullInt64{Int64: u.ID, Valid: true}, TariffID: sql.NullInt64{Int64: tariff.ID, Valid: true}, TariffName: "x", Amount: amount, Currency: "XTR", CreatedAt: now.Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB.ExecContext(ctx, "UPDATE payments SET status = 'applied', applied_at = $1 WHERE id = $2", now.Unix(), p.ID); err != nil {
			t.Fatal(err)
		}
	}
	pay(101, "new", 150)
	pay(101, "renew", 150)
	pay(101, "renew", 150)
	pay(102, "trial", 10)
	pay(103, "package", 75)

	stats, err := c.Stats(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	s := stats[vk.ID]
	if s == nil || s.Came != 2 || s.Bought != 2 || s.Renewed != 1 || s.Revenue["XTR"] != 460 {
		t.Fatalf("vk: %+v", s)
	}
	if s := stats[tg.ID]; s == nil || s.Came != 1 || s.Bought != 0 || s.Revenue["XTR"] != 75 {
		t.Fatalf("tg: %+v", s)
	}
	// A period after it all: nothing.
	later, err := c.Stats(ctx, now.Add(time.Hour))
	if err != nil || len(later) != 0 {
		t.Fatalf("later: %+v %v", later, err)
	}
}
