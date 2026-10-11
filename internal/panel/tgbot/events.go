package tgbot

import (
	"context"
	"encoding/json"
	"strconv"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
)

// The screens run as the bot addon and the panel tells them what the buyer and the
// subscriber must hear through its feed of events: the payment messages (Notifications on
// the panel's side, as billing's Notifier) and the term and traffic notices (emitNotices).
// HandleEvent is the addon's side.

// Events of the payment messages.
const (
	EventPaid          = "bot.paid"
	EventRefunded      = "bot.refunded"
	EventTrialNotGiven = "bot.trial_not_given"
)

// PaymentMessage is what a payment message is about.
type PaymentMessage struct {
	Payment  db.Payment `json:"payment"`
	User     db.User    `json:"user"`
	Created  bool       `json:"created,omitempty"`  // bot.paid: the payment made the subscription
	Disabled bool       `json:"disabled,omitempty"` // bot.refunded: the subscription was turned off
}

// Notifications is billing's Notifier as events for the bot addon.
type Notifications struct {
	Emit func(ctx context.Context, typ string, userID, tgID int64, data any, dedupe string) error
	Log  interface {
		Error(msg string, args ...any)
	}
}

func (n Notifications) emit(ctx context.Context, typ string, m PaymentMessage) {
	if n.Emit == nil {
		return
	}
	// Told once per payment and kind, whoever calls again (a webhook and Telegram's update).
	if err := n.Emit(ctx, typ, m.User.ID, m.Payment.TgID, m, typ+":"+strconv.FormatInt(m.Payment.ID, 10)); err != nil && n.Log != nil {
		n.Log.Error("telegram: payment message event", "payment", m.Payment.ID, "err", err)
	}
}

func (n Notifications) Paid(ctx context.Context, p db.Payment, u db.User, created bool) {
	n.emit(ctx, EventPaid, PaymentMessage{Payment: p, User: u, Created: created})
}

func (n Notifications) Refunded(ctx context.Context, p db.Payment, u db.User, disabled bool) {
	n.emit(ctx, EventRefunded, PaymentMessage{Payment: p, User: u, Disabled: disabled})
}

func (n Notifications) TrialNotGiven(ctx context.Context, p db.Payment) {
	n.emit(ctx, EventTrialNotGiven, PaymentMessage{Payment: p})
}

// EventTypes are the events HandleEvent acts on.
var EventTypes = []string{EventPaid, EventRefunded, EventTrialNotGiven, EventExpiring, EventExpired, EventTraffic90, EventTraffic100}

// HandleEvent sends what an event of the panel asks the bot to say. An error leaves the
// event unacknowledged, to come again.
func (b *Bot) HandleEvent(ctx context.Context, e events.Event) error {
	switch e.Type {
	case EventPaid, EventRefunded, EventTrialNotGiven:
		var m PaymentMessage
		if err := json.Unmarshal(e.Data, &m); err != nil {
			return nil // nothing to say from it, now or later
		}
		switch e.Type {
		case EventPaid:
			b.Paid(ctx, m.Payment, m.User, m.Created)
		case EventRefunded:
			b.Refunded(ctx, m.Payment, m.User, m.Disabled)
		default:
			b.TrialNotGiven(ctx, m.Payment)
		}
	case EventExpiring, EventExpired, EventTraffic90, EventTraffic100:
		out := b.d.Gateway.Outbox()
		if out == nil || e.TgID == 0 {
			return nil
		}
		var n NoticeEvent
		_ = json.Unmarshal(e.Data, &n)
		kind := noticeKind(e.Type, n.Days)
		u, err := b.d.Backend.User(ctx, e.UserID)
		if err != nil {
			return err
		}
		cfg := b.Config(ctx)
		now := b.d.Now()
		text, kb := b.noticeMessage(ctx, cfg, wordsFor(cfg.Lang), u, kind, now)
		silent, chat := cfg.QuietNight && night(now), e.TgID
		out.Notice(chat, func(ctx context.Context, c *tgate.Client) error {
			_, err := c.Send(ctx, chat, text, kb, silent)
			return err
		}, nil)
	}
	return nil
}
