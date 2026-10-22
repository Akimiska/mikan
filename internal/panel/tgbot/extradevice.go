package tgbot

import (
	"context"
	"fmt"
	"html"
	"strings"

	"mikan/internal/panel/model"
	"mikan/internal/tgapi"
)

// One more device for the shown subscription. Callback data: e the offer, ep:<p> pay for
// it (<p> as in the shop).

// deviceOffer is one more device for the subscription; ok is false when there is none.
func (b *Bot) deviceOffer(ctx context.Context, userID int64) (model.DeviceOffer, model.Available, bool) {
	o, av, ok, err := b.d.Backend.Shop().DeviceOfferFor(ctx, userID)
	if err != nil && !ok {
		return o, av, false
	}
	return o, av, ok
}

// extraDevice draws the screens of buying one more device for subscription u.
func (b *Bot) extraDevice(ctx context.Context, w *words, chat int64, u model.User, cmd, arg, notice string) (string, *tgapi.Keyboard) {
	back := []tgapi.Button{{Text: w.back, CallbackData: "s"}}
	if cmd == "ep" {
		return b.deviceInvoice(ctx, w, chat, u.ID, arg, []tgapi.Button{{Text: w.back, CallbackData: "e"}})
	}
	o, av, ok := b.deviceOffer(ctx, u.ID)
	if !ok {
		return html.EscapeString(w.deviceGone), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	lines := []string{"<b>" + html.EscapeString(fmt.Sprintf(w.deviceTitle, u.Name)) + "</b>", "",
		html.EscapeString(fmt.Sprintf(w.deviceNow, o.Limit, o.Limit+1))}
	switch {
	case o.Keep:
		lines = append(lines, html.EscapeString(w.deviceKeep))
	case o.Expiry.Valid:
		lines = append(lines, html.EscapeString(fmt.Sprintf(w.deviceUntil, w.date(model.Time(o.Expiry.Int64)))))
	}
	if left := o.Max - o.Extra; left > 0 {
		lines = append(lines, html.EscapeString(fmt.Sprintf(w.deviceLeft, left)))
	}
	if notice != "" {
		lines = append([]string{html.EscapeString(notice), ""}, lines...)
	}
	lines = append(lines, "", w.payHow)
	return strings.Join(lines, "\n"), &tgapi.Keyboard{InlineKeyboard: append(w.payButtons("ep:", o.Stars, o.Rub, av, b.addonName(ctx)), back)}
}

// deviceInvoice opens the invoice for one more device; like shopInvoice it runs off the
// update loop.
func (b *Bot) deviceInvoice(ctx context.Context, w *words, chat, userID int64, code string, back []tgapi.Button) (string, *tgapi.Keyboard) {
	provider, ok := providerOf(code)
	if !ok {
		return html.EscapeString(w.payUnavailable), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	p, err := b.d.Backend.Shop().DeviceInvoice(ctx, model.DeviceRequest{TgID: chat, UserID: userID, Provider: provider})
	if err != nil {
		msg := w.payError(err)
		if msg == w.notForSale {
			msg = w.deviceGone
		}
		return html.EscapeString(msg), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(p.TariffName), html.EscapeString(w.price(p.Amount, p.Currency)), w.payDevice)
	if p.Provider == model.Balance {
		return w.balancePaidScreen(p.Amount, p.Currency)
	}
	return text, &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}
