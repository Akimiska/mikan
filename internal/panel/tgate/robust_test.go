package tgate

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/settings"
	"mikan/internal/tgapi"
)

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The whole pre-checkout answer, the lookup and the call, fits in Telegram's ten seconds
// (the call's own time is tgapi's).
func TestPreCheckoutDeadline(t *testing.T) {
	if preCheckoutDeadline >= 10*time.Second {
		t.Fatalf("the pre-checkout answer has %s, Telegram gives ten seconds", preCheckoutDeadline)
	}
}

// The saved offset keeps a restart from receiving the last batch again, and a payment
// that could not be applied is offered again, in order, until it is.
func TestOffsetIsSavedAndPaymentsAreRetried(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(_ context.Context, tg int64, payload, charge, currency string, amount int64) error {
			if tries.Add(1) <= 2 {
				return errors.New("database is locked")
			}
			return nil
		}
	})
	pay := tgapi.Update{Message: &tgapi.Message{MessageID: 9, From: &tgapi.User{ID: 555}, Chat: tgapi.Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}}
	e.tg.push(pay)
	e.say(555, "привет") // comes after the payment and waits for it
	n := e.tg.count()
	until(t, "the payment is applied on the third try", func() bool { return tries.Load() == 3 })
	e.tg.wait(t, n, "sendMessage") // the chat message is handled after it
	if tries.Load() != 3 {
		t.Fatalf("an applied payment was offered again: %d tries", tries.Load())
	}
	var rec offsetRecord
	until(t, "the offset is saved", func() bool {
		r, ok, _ := settings.Get[offsetRecord](e.ctx, e.set, KeyOffset)
		rec = r
		return ok && rec.Bot == 1 && rec.Offset >= 1002
	})

	// A restart goes on from there, not from the beginning.
	e.tg.mu.Lock()
	before := len(e.tg.offsets)
	e.tg.mu.Unlock()
	e.gate.Reload()
	until(t, "the restarted bot polls", func() bool {
		e.tg.mu.Lock()
		defer e.tg.mu.Unlock()
		return len(e.tg.offsets) > before+1
	})
	e.tg.mu.Lock()
	first := e.tg.offsets[before:]
	e.tg.mu.Unlock()
	for _, o := range first {
		if o < rec.Offset {
			t.Fatalf("a restarted bot asked from %d, the saved offset is %d: %v", o, rec.Offset, first)
		}
	}
}

// An update that never works is dropped after a few tries (a Stars payment nothing saved
// after many more), and does not stop the bot.
func TestPoisonUpdateDoesNotStopTheBot(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(context.Context, int64, string, string, string, int64) error {
			tries.Add(1)
			return errors.New("always")
		}
	})
	e.tg.push(tgapi.Update{Message: &tgapi.Message{MessageID: 9, From: &tgapi.User{ID: 555}, Chat: tgapi.Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}})
	n := e.tg.count()
	e.say(555, "привет")
	e.tg.wait(t, n, "sendMessage")
	if got := tries.Load(); got != maxPaymentTries {
		t.Fatalf("tried %d times, want %d", got, maxPaymentTries)
	}
}

// A payment saved with its charge but not applied is not offered again: the reconcile
// pass finishes it.
func TestRecordedPaymentIsNotRetried(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(context.Context, int64, string, string, string, int64) error {
			tries.Add(1)
			return fmt.Errorf("%w: apply failed", billing.ErrRecorded)
		}
	})
	e.tg.push(tgapi.Update{Message: &tgapi.Message{MessageID: 9, From: &tgapi.User{ID: 555}, Chat: tgapi.Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}})
	n := e.tg.count()
	e.say(555, "привет")
	e.tg.wait(t, n, "sendMessage")
	if got := tries.Load(); got != 1 {
		t.Fatalf("a saved payment was offered %d times", got)
	}
}

// A payment that matches no invoice never will: not offered again.
func TestPaymentWithoutInvoiceIsNotRetried(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(context.Context, int64, string, string, string, int64) error {
			tries.Add(1)
			return billing.ErrBadPayment
		}
	})
	e.tg.push(tgapi.Update{Message: &tgapi.Message{MessageID: 9, From: &tgapi.User{ID: 555}, Chat: tgapi.Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}})
	n := e.tg.count()
	e.say(555, "привет")
	e.tg.wait(t, n, "sendMessage")
	if tries.Load() != 1 {
		t.Fatalf("an unmatched payment was tried %d times", tries.Load())
	}
}

// Run returns only when the poll loop, the outbox and the calls it started have: the
// database is closed after it.
func TestRunWaitsForItsWorkers(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	b := New(e.gate.d)
	returned := make(chan struct{})
	go func() { b.Run(ctx); close(returned) }()
	until(t, "the second bot polls", func() bool { return b.Status().Running })
	if b.out.Load() == nil {
		t.Fatal("no outbox while running")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if b.out.Load() != nil || b.client.Load() != nil {
		t.Fatal("Run returned while the poll loop was still running")
	}
}
