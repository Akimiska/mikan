package billing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

func (e *env) events() []db.AddonEvent {
	e.t.Helper()
	evs, err := e.st.Q.ListAddonEvents(context.Background(), db.ListAddonEventsParams{Types: []string{}, Lim: 100})
	must(e.t, err)
	return evs
}

// The addons hear of an applied payment and of its refund once each, from the transaction
// that made the change, with who paid and what.
func TestPaymentEvents(t *testing.T) {
	e := newEnv(t)
	hub := events.NewHub()
	e.s.d.Events = hub
	woken := hub.Wait()
	p := e.buy(0, "ch-events")
	select {
	case <-woken:
	default:
		t.Fatal("a waiting reader is not woken")
	}
	evs := e.events()
	if len(evs) != 1 || evs[0].Type != events.PaymentPaid || evs[0].TgID.Int64 != 555 || evs[0].UserID.Int64 != p.UserID.Int64 {
		t.Fatalf("events after the payment: %+v", evs)
	}
	var got PaymentEvent
	must(t, json.Unmarshal([]byte(evs[0].Data), &got))
	if got.PaymentID != p.ID || got.Amount != p.Amount || got.Currency != p.Currency || got.Provider != Stars || !got.Created {
		t.Fatalf("payment event: %+v", got)
	}

	// Applying it again (reconcile, a retried update) tells nothing new.
	must(t, e.s.Apply(context.Background(), p.ID))
	if n := len(e.events()); n != 1 {
		t.Fatalf("%d events after a second apply", n)
	}

	e.now = e.now.Add(time.Hour)
	_, err := e.s.Refund(context.Background(), p.ID)
	must(t, err)
	_, _ = e.s.Refund(context.Background(), p.ID) // a second refund changes nothing, whatever it answers
	evs = e.events()
	if len(evs) != 2 || evs[1].Type != events.PaymentRefunded || evs[1].ID <= evs[0].ID {
		t.Fatalf("events after the refund: %+v", evs)
	}
}
