package billing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// offerFree turns the free plan on, on the seeded trial tariff (3 days), with selling off:
// the free plan takes no payment.
func (e *env) offerFree(condition string) db.Tariff {
	e.t.Helper()
	ctx := context.Background()
	ts, err := e.st.Q.ListTariffs(ctx)
	must(e.t, err)
	free := ts[0]
	must(e.t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{FreeTariffID: free.ID, FreeCondition: condition}))
	return free
}

// The free plan is taken again only once the previous free subscription has ended; it is
// counted, and its users come from the free plan.
func TestFreePlanAgainAfterTheEnd(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.s.FreePlan(ctx, 911); !errors.Is(err, ErrFreeOff) {
		t.Fatalf("a free plan nobody offers: %v", err)
	}
	if _, ok := e.s.FreeOffer(ctx, 911); ok {
		t.Fatal("a free plan nobody offers is on offer")
	}
	free := e.offerFree(FreeNoCondition)
	if _, ok := e.s.FreeOffer(ctx, 911); !ok {
		t.Fatal("the free plan is not offered to a new account")
	}
	u, err := e.s.FreePlan(ctx, 911)
	must(t, err)
	if u.TariffID.Int64 != free.ID || u.Source != domain.UserFromFree || u.ExpiresAt.Int64 != e.now.Add(time.Duration(free.DurationDays)*24*time.Hour).Unix() {
		t.Fatalf("free user %+v on %+v", u, free)
	}
	if links, err := e.st.Q.ListTgLinksOf(ctx, 911); err != nil || len(links) != 1 || links[0].ID != u.ID {
		t.Fatalf("the free subscription is not linked: %+v %v", links, err)
	}
	if _, err := e.s.FreePlan(ctx, 911); !errors.Is(err, ErrFreeActive) {
		t.Fatalf("a second free plan while the first runs: %v", err)
	}
	if _, ok := e.s.FreeOffer(ctx, 911); ok {
		t.Fatal("the free plan is offered while the first runs")
	}
	// The term is over: another one.
	e.now = e.now.Add(time.Duration(free.DurationDays)*24*time.Hour + time.Second)
	if _, ok := e.s.FreeOffer(ctx, 911); !ok {
		t.Fatal("the free plan is not offered after the first ended")
	}
	again, err := e.s.FreePlan(ctx, 911)
	must(t, err)
	if again.ID == u.ID {
		t.Fatal("the same subscription again")
	}
	// A deleted free subscription has ended too.
	_, err = e.st.DB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", again.ID)
	must(t, err)
	_, err = e.s.FreePlan(ctx, 911)
	must(t, err)
	if n, _ := e.st.Q.CountFreePlans(ctx); n != 3 {
		t.Fatalf("free plans counted %d", n)
	}
	// The trial is another thing: the account still may not have it, being a customer now,
	// and the free plan does not count as the trial.
	if taken, _ := e.st.Q.HasTrial(ctx, 911); taken {
		t.Fatal("the free plan took the trial")
	}
}

// Turning the free plan off hides it and stops it; the subscriptions it gave stay.
func TestFreePlanOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.offerFree(FreeNoCondition)
	u, err := e.s.FreePlan(ctx, 912)
	must(t, err)
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{}))
	if _, err := e.s.FreePlan(ctx, 913); !errors.Is(err, ErrFreeOff) {
		t.Fatalf("a free plan turned off: %v", err)
	}
	if _, err := e.st.Q.GetUser(ctx, u.ID); err != nil {
		t.Fatalf("the free subscription is gone with the plan: %v", err)
	}
}

// The channel condition: a member takes it, a stranger is told to join, no channel set up
// turns the plan off, and a channel the bot cannot ask about lets the account through.
func TestFreePlanChannel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.offerFree(FreeChannel)
	if _, ok := e.s.FreeOffer(ctx, 914); ok {
		t.Fatal("a free plan for a channel nobody set up is on offer")
	}
	if _, err := e.s.FreePlan(ctx, 914); !errors.Is(err, ErrFreeOff) {
		t.Fatalf("a free plan for a channel nobody set up: %v", err)
	}
	e.tg.mu.Lock()
	e.tg.channel, e.tg.members = "https://t.me/mikan_news", map[int64]bool{915: true}
	e.tg.mu.Unlock()
	if o, ok := e.s.FreeOffer(ctx, 914); !ok || o.Channel != "https://t.me/mikan_news" {
		t.Fatalf("the offer does not name the channel: %+v %v", o, ok)
	}
	if _, err := e.s.FreePlan(ctx, 914); !errors.Is(err, ErrFreeChannel) {
		t.Fatalf("a stranger to the channel: %v", err)
	}
	if links, _ := e.st.Q.ListTgLinksOf(ctx, 914); len(links) != 0 {
		t.Fatalf("a stranger got a subscription: %+v", links)
	}
	if _, err := e.s.FreePlan(ctx, 915); err != nil {
		t.Fatalf("a member: %v", err)
	}
	e.tg.mu.Lock()
	e.tg.memberErr = errors.New("member list is inaccessible")
	e.tg.mu.Unlock()
	if _, err := e.s.FreePlan(ctx, 914); err != nil {
		t.Fatalf("a channel the bot cannot ask about locked the account out: %v", err)
	}
}

// Two taps at once make one free subscription.
func TestFreePlanTapsAtOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.offerFree(FreeNoCondition)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { _, errs[i] = e.s.FreePlan(ctx, 916) })
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrFreeActive) {
			t.Fatalf("a tap failed: %v", err)
		}
	}
	links, _ := e.st.Q.ListTgLinksOf(ctx, 916)
	if ok != 1 || len(links) != 1 {
		t.Fatalf("%d free plans given, %d subscriptions", ok, len(links))
	}
}

// offerTrialAndFree turns on both the free trial and the free plan on the one seeded trial
// tariff, as the admin of the report of 2026-10-22 did.
func (e *env) offerTrialAndFree() db.Tariff {
	e.t.Helper()
	ctx := context.Background()
	ts, err := e.st.Q.ListTariffs(ctx)
	must(e.t, err)
	trial := ts[0]
	must(e.t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{TrialTariffID: trial.ID, FreeTariffID: trial.ID}))
	return trial
}

// The trial's subscription counts as the account's free one while it runs: the free plan on
// top of it was the same subscription twice, both on one tariff (reported 2026-10-22). The
// free plan comes once the trial has ended or its subscription is deleted, and the other way
// round the trial is not for an account the free plan gave a subscription.
func TestFreePlanWaitsForTheTrial(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	trial := e.offerTrialAndFree()
	if _, ok := e.s.FreeOffer(ctx, 931); !ok || !e.s.TrialOpen(ctx, 931) {
		t.Fatal("a new account is not offered the trial and the free plan")
	}
	u, err := e.s.Trial(ctx, 931)
	must(t, err)
	if _, ok := e.s.FreeOffer(ctx, 931); ok {
		t.Fatal("the free plan is offered while the trial runs")
	}
	if _, err := e.s.FreePlan(ctx, 931); !errors.Is(err, ErrFreeActive) {
		t.Fatalf("a free plan on top of the trial: %v", err)
	}
	if links, _ := e.st.Q.ListTgLinksOf(ctx, 931); len(links) != 1 || links[0].ID != u.ID {
		t.Fatalf("the account holds %+v, want the trial alone", links)
	}
	if n, _ := e.st.Q.CountFreePlans(ctx); n != 0 {
		t.Fatalf("free plans counted %d", n)
	}
	// Another account's trial holds nobody else back.
	if _, err := e.s.FreePlan(ctx, 932); err != nil {
		t.Fatalf("an account without a trial: %v", err)
	}
	// Free plan first: the trial is not for a customer, and nothing is made.
	if _, err := e.s.Trial(ctx, 932); !errors.Is(err, ErrTrialUsed) || e.s.TrialOpen(ctx, 932) {
		t.Fatalf("a trial on top of the free plan: %v", err)
	}
	if links, _ := e.st.Q.ListTgLinksOf(ctx, 932); len(links) != 1 {
		t.Fatalf("the free plan's account holds %d subscriptions", len(links))
	}
	// A deleted trial subscription has ended.
	gone, err := e.s.Trial(ctx, 933)
	must(t, err)
	_, err = e.st.DB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", gone.ID)
	must(t, err)
	if _, err := e.s.FreePlan(ctx, 933); err != nil {
		t.Fatalf("a free plan after the trial was deleted: %v", err)
	}
	// The trial is over: the free plan comes, a subscription of its own.
	e.now = e.now.Add(time.Duration(trial.DurationDays)*24*time.Hour + time.Second)
	if _, ok := e.s.FreeOffer(ctx, 931); !ok {
		t.Fatal("the free plan is not offered after the trial ended")
	}
	free, err := e.s.FreePlan(ctx, 931)
	must(t, err)
	if free.ID == u.ID || free.Source != domain.UserFromFree {
		t.Fatalf("free subscription %+v after the trial %+v", free, u)
	}
}

// A trial bought for a price counts the same: the free plan waits for it.
func TestFreePlanWaitsForThePaidTrial(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ts, err := e.st.Q.ListTariffs(ctx)
	must(t, err)
	trial := ts[0]
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, TrialTariffID: trial.ID, TrialStars: 1, FreeTariffID: trial.ID}))
	p, err := e.s.TrialInvoice(ctx, 934, Stars)
	must(t, err)
	must(t, e.s.PreCheckout(ctx, 934, p.Payload, "XTR", 1))
	must(t, e.s.StarsPaid(ctx, 934, p.Payload, "ch-paid-trial", "XTR", 1))
	if got := e.payment(p.ID); got.Status != PaymentApplied {
		t.Fatalf("trial payment %+v", got)
	}
	if _, ok := e.s.FreeOffer(ctx, 934); ok {
		t.Fatal("the free plan is offered while the bought trial runs")
	}
	if _, err := e.s.FreePlan(ctx, 934); !errors.Is(err, ErrFreeActive) {
		t.Fatalf("a free plan on top of the bought trial: %v", err)
	}
	if links, _ := e.st.Q.ListTgLinksOf(ctx, 934); len(links) != 1 {
		t.Fatalf("the account holds %d subscriptions", len(links))
	}
}

// The trial and the free plan tapped at once (two old messages, the bot and the Mini App)
// give the account one subscription: each side reads what the other writes on its own
// serializable transaction, so one of them sees the other's.
func TestTrialAndFreePlanAtOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.offerTrialAndFree()
	for tg := int64(940); tg < 950; tg++ {
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Go(func() {
				if i%2 == 0 {
					_, errs[i] = e.s.Trial(ctx, tg)
				} else {
					_, errs[i] = e.s.FreePlan(ctx, tg)
				}
			})
		}
		wg.Wait()
		given := 0
		for _, err := range errs {
			switch {
			case err == nil:
				given++
			case !errors.Is(err, ErrTrialUsed) && !errors.Is(err, ErrFreeActive):
				t.Fatalf("account %d: a tap failed: %v", tg, err)
			}
		}
		if links, _ := e.st.Q.ListTgLinksOf(ctx, tg); given != 1 || len(links) != 1 {
			t.Fatalf("account %d: %d given, %d subscriptions", tg, given, len(links))
		}
	}
}

// An account that holds as many subscriptions as it may gets no free one.
func TestFreePlanLinkLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.offerFree(FreeNoCondition)
	for range e.s.d.MaxLinks {
		u, err := e.s.d.Users.Create(ctx, domain.CreateInput{Name: "held", TariffID: e.sale.ID})
		must(t, err)
		must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 917, CreatedAt: e.now.Unix()}))
	}
	if _, ok := e.s.FreeOffer(ctx, 917); ok {
		t.Fatal("the free plan is offered past the limit")
	}
	if _, err := e.s.FreePlan(ctx, 917); !errors.Is(err, ErrTooManySubs) {
		t.Fatalf("a free plan past the limit: %v", err)
	}
}
