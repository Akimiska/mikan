package tgbot

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// event is what the addon pulls from the panel's feed: the payment or the notice, as JSON.
func event(t *testing.T, typ string, userID, tgID int64, data any) events.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return events.Event{ID: 1, Type: typ, UserID: userID, TgID: tgID, Data: raw}
}

// said handles an event and returns the message to the chat that has want in it (a fresh
// menu follows the payment messages).
func (e *env) said(ev events.Event, chat int64, want string) call {
	e.t.Helper()
	n := e.tg.count()
	if err := e.bot.HandleEvent(e.ctx, ev); err != nil {
		e.t.Fatal(err)
	}
	var got call
	e.tg.until(e.t, n, func(cs []call) bool {
		for _, c := range cs {
			if c.method == "sendMessage" && c.body["chat_id"] == float64(chat) && strings.Contains(text(c), want) {
				got = c
				return true
			}
		}
		return false
	})
	return got
}

// The payment messages come to the addon as events and say what the buyer needs to hear.
func TestHandleEventPaymentMessages(t *testing.T) {
	e := setup(t)
	const buyer = 4242
	if err := e.st.Q.UpsertTgChat(e.ctx, db.UpsertTgChatParams{TgID: buyer, FirstName: "Buyer", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	pay := db.Payment{ID: 7, TgID: buyer, TariffName: "Месяц", Amount: 100, Currency: "RUB"}

	// bot.paid: a new subscription gets its link, and becomes the one the chat shows.
	msg := e.said(event(t, EventPaid, e.user.ID, buyer, PaymentMessage{Payment: pay, User: e.user, Created: true}), buyer, "готова")
	if !strings.Contains(text(msg), "подписка «Анна» готова") || !strings.Contains(text(msg), e.user.SubToken) {
		t.Fatalf("paid, new: %q", text(msg))
	}
	if ch, err := e.st.Q.GetTgChat(e.ctx, buyer); err != nil || ch.Current != e.user.ID {
		t.Fatalf("the paid subscription is not the chat's current one: %+v %v", ch, err)
	}
	msg = e.said(event(t, EventPaid, e.user.ID, buyer, PaymentMessage{Payment: pay, User: e.user}), buyer, "продлена")
	if !strings.Contains(text(msg), "продлена") {
		t.Fatalf("paid, renewed: %q", text(msg))
	}
	pkg := pay
	pkg.Kind = billing.KindPackage
	msg = e.said(event(t, EventPaid, e.user.ID, buyer, PaymentMessage{Payment: pkg, User: e.user}), buyer, "начислен")
	if !strings.Contains(text(msg), "пакет «Месяц» начислен") {
		t.Fatalf("paid, package: %q", text(msg))
	}

	// bot.refunded: what was given is taken back, and the buyer is told which.
	msg = e.said(event(t, EventRefunded, e.user.ID, buyer, PaymentMessage{Payment: pay, User: e.user, Disabled: true}), buyer, "отключена")
	if !strings.Contains(text(msg), "Подписка «Анна» отключена") {
		t.Fatalf("refunded, turned off: %q", text(msg))
	}
	msg = e.said(event(t, EventRefunded, e.user.ID, buyer, PaymentMessage{Payment: pay, User: e.user}), buyer, "продление")
	if !strings.Contains(text(msg), "продление") {
		t.Fatalf("refunded, renewal: %q", text(msg))
	}
	msg = e.said(event(t, EventRefunded, 0, buyer, PaymentMessage{Payment: pay}), buyer, "возвращён.")
	if !strings.Contains(text(msg), "Платёж за «Месяц» возвращён.") {
		t.Fatalf("refunded, subscription gone: %q", text(msg))
	}

	// bot.trial_not_given: why, and where the money comes back, with the support link.
	msg = e.said(event(t, EventTrialNotGiven, 0, buyer, PaymentMessage{Payment: pay}), buyer, "не засчитан")
	if !strings.Contains(text(msg), "платёж 1 ₽ не засчитан") || buttons(msg)["💬 Поддержка"] != "https://t.me/mikan_support" {
		t.Fatalf("trial not given: %q %v", text(msg), buttons(msg))
	}

	// An event that says nothing readable is let go, not retried for ever.
	if err := e.bot.HandleEvent(e.ctx, events.Event{Type: EventPaid, Data: json.RawMessage(`"x"`)}); err != nil {
		t.Fatalf("an unreadable event: %v", err)
	}
}

// The notices come as events too: the addon words them as the admin did and sends them to
// the chat the event names.
func TestHandleEventNotices(t *testing.T) {
	e := setup(t)
	const anna = 555
	e.link(anna)
	e.expireIn(2 * 24 * time.Hour)
	u, _ := e.st.Q.GetUser(e.ctx, e.user.ID)

	msg := e.said(event(t, EventExpiring, u.ID, anna, NoticeEvent{Days: 3, ExpiresAt: u.ExpiresAt.Int64}), anna, "заканчивается")
	if !strings.Contains(text(msg), "Подписка «Анна» заканчивается") || buttons(msg)["💳 Продлить"] != "r" {
		t.Fatalf("expiring: %q %v", text(msg), buttons(msg))
	}
	e.expireIn(-time.Hour)
	msg = e.said(event(t, EventExpired, u.ID, anna, NoticeEvent{ExpiresAt: u.ExpiresAt.Int64}), anna, "закончилась")
	if !strings.Contains(text(msg), "закончилась") || buttons(msg)["💳 Продлить"] != "r" {
		t.Fatalf("expired: %q %v", text(msg), buttons(msg))
	}
	// Traffic: 90% has no renew button, the end of it has; neither offers packages here, as
	// nothing is for sale.
	msg = e.said(event(t, EventTraffic90, u.ID, anna, NoticeEvent{PeriodFrom: u.PeriodStart}), anna, "90%")
	if !strings.Contains(text(msg), "90% трафика") || len(buttons(msg)) != 0 {
		t.Fatalf("traffic 90: %q %v", text(msg), buttons(msg))
	}
	msg = e.said(event(t, EventTraffic100, u.ID, anna, NoticeEvent{PeriodFrom: u.PeriodStart}), anna, "на этот период")
	if !strings.Contains(text(msg), "на этот период закончился") || buttons(msg)["💳 Продлить"] != "r" {
		t.Fatalf("traffic 100: %q %v", text(msg), buttons(msg))
	}
	// The night's notices come without a sound.
	e.mu.Lock()
	e.now = time.Date(2027, 1, 15, 2, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	e.mu.Unlock()
	msg = e.said(event(t, EventExpiring, u.ID, anna, NoticeEvent{Days: 1}), anna, "заканчивается")
	if msg.body["disable_notification"] != true {
		t.Fatalf("a night notice rings: %v", msg.body)
	}
	// No account to tell, nothing to send.
	n := e.tg.count()
	if err := e.bot.HandleEvent(e.ctx, event(t, EventExpiring, u.ID, 0, NoticeEvent{Days: 3})); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if e.tg.count() != n {
		t.Fatal("a notice for nobody was sent")
	}
	// A subscription that cannot be read is an error: the event is not acknowledged.
	if err := e.bot.HandleEvent(e.ctx, event(t, EventExpiring, 999999, anna, NoticeEvent{Days: 3})); err == nil {
		t.Fatal("an event about a missing subscription is acknowledged")
	}
}

// The panel tells the addon of a notice through Notifications' Emit and the addon says it:
// the same events, end to end on the panel's side.
func TestPaymentMessagesReachTheAddonThroughNotifications(t *testing.T) {
	e := setup(t)
	const buyer = 4242
	var told atomic.Int32
	n := Notifications{Log: e.panel.d.Log, Emit: func(ctx context.Context, typ string, userID, tgID int64, data any, dedupe string) error {
		told.Add(1)
		if want := typ + ":" + strconv.FormatInt(7, 10); dedupe != want {
			t.Errorf("dedupe %q, want %q", dedupe, want)
		}
		return e.emit(ctx, typ, userID, tgID, data, dedupe)
	}}
	from := e.tg.count()
	n.TrialNotGiven(e.ctx, db.Payment{ID: 7, TgID: buyer, Amount: 100, Currency: "RUB"})
	e.tg.wait(t, from, "sendMessage")
	if told.Load() != 1 {
		t.Fatalf("told %d times", told.Load())
	}
}
