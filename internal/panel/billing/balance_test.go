package billing

import (
	"context"
	"errors"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// balanceEnv: the balance on in dollars at 95 ₽ for 1 $, top-ups from 1 $ to 100 $.
func balanceEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	c := e.s.Config(ctx)
	c.Balance = BalanceConfig{Enabled: true, Currency: "USD", Rate: 95, Details: "Card 0000", Min: 100, Max: 10000}
	must(t, settings.Set(ctx, settings.New(e.st.Q), KeyConfig, c))
	return e
}

// A top-up goes on the balance once the admin approves it, and only once; a rejected one
// gives nothing. The account is told either way.
func TestTopup(t *testing.T) {
	e := balanceEnv(t)
	ctx := context.Background()
	var told []db.Topup
	e.s.d.TopupWaiting = func(_ context.Context, t db.Topup) { told = append(told, t) }
	if _, err := e.s.StartTopup(ctx, 555, 50); !errors.Is(err, ErrTopupAmount) {
		t.Fatalf("under the minimum: %v", err)
	}
	if _, err := e.s.SubmitTopup(ctx, 555, "photo-1"); !errors.Is(err, ErrTopupNone) {
		t.Fatalf("a screenshot without a top-up: %v", err)
	}
	must(t, firstErr(e.s.StartTopup(ctx, 555, 0)))
	if _, err := e.s.SubmitTopup(ctx, 555, "photo-1"); !errors.Is(err, ErrTopupNone) {
		t.Fatalf("a screenshot before the amount: %v", err)
	}
	if _, err := e.s.SetTopupAmount(ctx, 555, 20000); !errors.Is(err, ErrTopupAmount) {
		t.Fatalf("over the maximum: %v", err)
	}
	must(t, firstErr(e.s.SetTopupAmount(ctx, 555, 2500)))
	sent, err := e.s.SubmitTopup(ctx, 555, "photo-1")
	if err != nil || sent.Status != TopupPending || len(told) != 1 || told[0].Amount != 2500 {
		t.Fatalf("submitted: %+v %v, told %+v", sent, err, told)
	}
	if _, ok := e.s.TopupDraft(ctx, 555); ok {
		t.Fatal("still a draft")
	}
	got, balance, err := e.s.DecideTopup(ctx, sent.ID, true, 1, "")
	if err != nil || got.Status != TopupApproved || balance != 2500 {
		t.Fatalf("approve: %+v %d %v", got, balance, err)
	}
	if _, _, err := e.s.DecideTopup(ctx, sent.ID, true, 1, ""); !errors.Is(err, ErrTopupGone) {
		t.Fatalf("approved twice: %v", err)
	}
	if n, _ := e.s.BalanceOf(ctx, 555); n != 2500 {
		t.Fatalf("balance: %d", n)
	}
	must(t, firstErr(e.s.StartTopup(ctx, 555, 1000)))
	second, err := e.s.SubmitTopup(ctx, 555, "photo-2")
	must(t, err)
	if _, balance, err := e.s.DecideTopup(ctx, second.ID, false, 1, "no transfer"); err != nil || balance != 2500 {
		t.Fatalf("reject: %d %v", balance, err)
	}
	evs, _ := e.st.Q.ListAddonEvents(ctx, db.ListAddonEventsParams{Lim: 100})
	var topups int
	for _, ev := range evs {
		if ev.Type == "balance.topup" && ev.TgID.Int64 == 555 {
			topups++
		}
	}
	if topups != 2 {
		t.Fatalf("the account is told of each decision: %d", topups)
	}
}

// The balance pays a tariff at its ruble price in its own currency, at once; without
// enough the payment fails and nothing is taken. A refund puts the money back.
func TestPayFromBalance(t *testing.T) {
	e := balanceEnv(t)
	ctx := context.Background()
	u, err := e.s.d.Users.Create(ctx, domain.CreateInput{Name: "mine", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: 1}))
	// 199 ₽ at 95 ₽ for 1 $ is 2.10 $ (rounded up).
	if _, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, UserID: u.ID, TariffID: e.sale.ID, Provider: Balance}); !errors.Is(err, ErrBalanceLow) {
		t.Fatalf("an empty balance: %v", err)
	}
	if _, err := e.s.AdjustBalance(ctx, 555, 500, 1, "gift"); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.Invoice(ctx, InvoiceRequest{TgID: 555, UserID: u.ID, TariffID: e.sale.ID, Provider: Balance})
	if err != nil || p.Status != PaymentApplied || p.Amount != 210 || p.Currency != "USD" {
		t.Fatalf("paid: %+v %v", p, err)
	}
	if n, _ := e.s.BalanceOf(ctx, 555); n != 290 {
		t.Fatalf("left: %d", n)
	}
	if _, err := e.s.Refund(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Refund(ctx, p.ID); !errors.Is(err, ErrNotRefunable) {
		t.Fatalf("refunded twice: %v", err)
	}
	if n, _ := e.s.BalanceOf(ctx, 555); n != 500 {
		t.Fatalf("after the refund: %d", n)
	}
	if _, err := e.s.AdjustBalance(ctx, 555, -600, 1, "too much"); !errors.Is(err, ErrBalanceLow) {
		t.Fatalf("below zero: %v", err)
	}
	ops, _ := e.s.BalanceHistory(ctx, 555, 10)
	if len(ops) != 3 || ops[0].Kind != OpRefund || ops[1].Kind != OpSpend || ops[2].Kind != OpAdjust {
		t.Fatalf("history: %+v", ops)
	}
}

func TestFromRub(t *testing.T) {
	for _, c := range []struct {
		cfg  BalanceConfig
		rub  int64
		want int64
	}{
		{BalanceConfig{Currency: "RUB", Rate: 7}, 19900, 19900},
		{BalanceConfig{Currency: "USD", Rate: 95}, 19900, 210},
		{BalanceConfig{Currency: "BYN", Rate: 30}, 9000, 300},
	} {
		if got := c.cfg.FromRub(c.rub); got != c.want {
			t.Errorf("%+v FromRub(%d) = %d, want %d", c.cfg, c.rub, got, c.want)
		}
	}
}

func firstErr[T any](_ T, err error) error { return err }
