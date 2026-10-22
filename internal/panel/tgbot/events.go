package tgbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
	"mikan/internal/tgapi"
)

// The screens run as the bot addon and the panel tells them what the buyer and the
// subscriber must hear through its feed of events: the payment messages (events.PaymentPaid,
// PaymentRefunded and TrialNotGiven, written by billing in the transaction of the change) and
// the term and traffic notices (botpanel.Panel.Notify). HandleEvent is the addon's side: it
// takes the subscription over the Panel API, as any addon would.

// EventTypes are the events HandleEvent acts on.
var EventTypes = []string{events.PaymentPaid, events.PaymentRefunded, events.TrialNotGiven,
	events.UserExpiring, events.UserExpired, events.Traffic90, events.Traffic100, events.BalanceTopup}

// gone: the subscription of a payment is not there for the bot any more: deleted, or moved to
// an account that is not talking to it. Retrying would not change that.
func gone(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, model.ErrNotFound) || errors.Is(err, ErrNotTalking)
}

// HandleEvent sends what an event of the panel asks the bot to say. An error leaves the
// event unacknowledged, to come again.
func (b *Bot) HandleEvent(ctx context.Context, e events.Event) error {
	switch e.Type {
	case events.PaymentPaid, events.PaymentRefunded:
		var p events.Payment
		if err := json.Unmarshal(e.Data, &p); err != nil || e.TgID == 0 {
			return nil // nothing to say from it, now or later
		}
		var u model.User
		if e.UserID != 0 {
			var err error
			if u, err = b.d.Backend.User(ctx, e.UserID); err != nil {
				if !gone(err) {
					return err
				}
				u = model.User{} // deleted since: a refund still says the money is back
			}
		}
		if e.Type == events.PaymentPaid {
			if u.ID == 0 {
				return nil // a subscription that is gone has nothing to be thanked for
			}
			b.paid(ctx, e.TgID, p, u)
		} else {
			b.refunded(ctx, e.TgID, p, u)
		}
	case events.BalanceTopup:
		var t events.Topup
		if err := json.Unmarshal(e.Data, &t); err != nil || e.TgID == 0 {
			return nil
		}
		b.topupDecided(ctx, e.TgID, t)
	case events.TrialNotGiven:
		var n events.NotGiven
		if err := json.Unmarshal(e.Data, &n); err != nil || e.TgID == 0 {
			return nil
		}
		// A trial that went back by itself is told by its PaymentRefunded; only the one the
		// admin must return by hand has no other word.
		if n.Refund == events.RefundedManually {
			b.trialNotGiven(ctx, e.TgID, n)
		}
	case events.UserExpiring, events.UserExpired, events.Traffic90, events.Traffic100:
		if e.TgID == 0 {
			return nil
		}
		var n events.Notice
		_ = json.Unmarshal(e.Data, &n)
		kind := noticeKind(e.Type, n.Days)
		u, err := b.d.Backend.User(ctx, e.UserID)
		if err != nil {
			return err
		}
		cfg := b.config(ctx)
		now := b.d.Now()
		text, kb := b.noticeMessage(ctx, cfg, wordsFor(cfg.Lang), u, kind, now)
		silent, chat := cfg.QuietNight && night(now), e.TgID
		b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgapi.Client) error {
			_, err := c.Send(ctx, chat, text, kb, silent)
			return err
		}, nil)
	}
	return nil
}
