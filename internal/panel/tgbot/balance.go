package tgbot

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
	"mikan/internal/tgapi"
)

// The account's balance and its top-ups by hand. Callback data: bl the balance, bt a
// top-up (the amount comes as a message or a button), ba:<minor units> that amount, bx
// drop the top-up. A top-up with its amount waits for a screenshot of the transfer: the
// next picture in the chat is it.

// topupSteps are the amounts offered as buttons, in units of the balance's currency.
var topupSteps = []int64{100, 300, 500, 1000}

// balanceOn: the balance takes top-ups and pays now.
func (b *Bot) balanceOn(ctx context.Context) (model.BalanceConfig, bool) {
	c := b.d.Backend.Shop().Config(ctx).Balance
	return c, c.Enabled && c.On()
}

// balanceButton is the menu's entry, with what is on the balance.
func (b *Bot) balanceButton(ctx context.Context, w *words, chat int64) (tgapi.Button, bool) {
	c, ok := b.balanceOn(ctx)
	if !ok {
		return tgapi.Button{}, false
	}
	n, _ := b.d.Backend.Shop().BalanceOf(ctx, chat)
	return tgapi.Button{Text: fmt.Sprintf(w.balanceButton, w.price(n, c.Currency)), CallbackData: "bl"}, true
}

// balanceScreen draws bl, bt and ba.
func (b *Bot) balanceScreen(ctx context.Context, w *words, chat int64, cmd, notice string) (string, *tgapi.Keyboard) {
	home := []tgapi.Button{{Text: w.back, CallbackData: "m"}}
	c, ok := b.balanceOn(ctx)
	if !ok {
		return html.EscapeString(w.balanceOff), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{home}}
	}
	shop := b.d.Backend.Shop()
	lines := []string{}
	if notice != "" {
		lines = append(lines, html.EscapeString(notice), "")
	}
	if t, ok := shop.TopupDraft(ctx, chat); ok && cmd != "bl" {
		cancel := []tgapi.Button{{Text: w.cancel, CallbackData: "bx"}}
		if t.Amount == 0 {
			lines = append(lines, "<b>"+html.EscapeString(w.topupTitle)+"</b>", "", html.EscapeString(w.topupAmountAsk))
			if c.Min > 0 || c.Max > 0 {
				lines = append(lines, html.EscapeString(w.topupBounds(c)))
			}
			var row []tgapi.Button
			for _, step := range topupSteps {
				amount := step * 100
				if c.Min > 0 && amount < c.Min || c.Max > 0 && amount > c.Max {
					continue
				}
				row = append(row, tgapi.Button{Text: w.price(amount, c.Currency), CallbackData: "ba:" + strconv.FormatInt(amount, 10)})
			}
			rows := [][]tgapi.Button{}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			return strings.Join(lines, "\n"), &tgapi.Keyboard{InlineKeyboard: append(rows, cancel)}
		}
		lines = append(lines, "<b>"+html.EscapeString(fmt.Sprintf(w.topupPay, w.price(t.Amount, t.Currency)))+"</b>", "",
			html.EscapeString(c.Details), "", html.EscapeString(w.topupShot))
		return strings.Join(lines, "\n"), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{cancel}}
	}
	n, _ := shop.BalanceOf(ctx, chat)
	lines = append(lines, "<b>"+html.EscapeString(fmt.Sprintf(w.balanceTitle, w.price(n, c.Currency)))+"</b>")
	if ops, err := shop.BalanceHistory(ctx, chat, 5); err == nil && len(ops) > 0 {
		lines = append(lines, "")
		for _, op := range ops {
			sign := "+"
			if op.Amount < 0 {
				sign = "−"
			}
			amount := op.Amount
			if amount < 0 {
				amount = -amount
			}
			line := sign + w.price(amount, c.Currency) + " · " + w.balanceOp(op.Kind)
			if op.Note != "" {
				line += " · " + op.Note
			}
			lines = append(lines, html.EscapeString(line))
		}
	}
	lines = append(lines, "", html.EscapeString(w.balanceHint))
	return strings.Join(lines, "\n"), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.topupButton, CallbackData: "bt"}}, home}}
}

// balanceAct does what bt, ba and bx change, before their screen is drawn.
func (b *Bot) balanceAct(ctx context.Context, chat int64, cmd, arg string) (screen, notice string) {
	w := wordsFor(b.config(ctx).Lang)
	shop := b.d.Backend.Shop()
	switch cmd {
	case "bt":
		if _, err := shop.StartTopup(ctx, chat, 0); err != nil {
			return "bl", w.topupError(err)
		}
		return "bt", ""
	case "ba":
		amount, _ := strconv.ParseInt(arg, 10, 64)
		if _, ok := shop.TopupDraft(ctx, chat); !ok {
			if _, err := shop.StartTopup(ctx, chat, amount); err != nil {
				return "bl", w.topupError(err)
			}
			return "bt", ""
		}
		if _, err := shop.SetTopupAmount(ctx, chat, amount); err != nil {
			return "bt", w.topupError(err)
		}
		return "bt", ""
	case "bx":
		_ = shop.CancelTopup(ctx, chat)
		return "bl", w.topupCancelled
	}
	return "bl", ""
}

// topupMessage takes a message for an open top-up: its amount, or the screenshot. ok is
// false when there is none, or the message is neither: the chat goes on as usual.
func (b *Bot) topupMessage(ctx context.Context, w *words, m *tgapi.Message) (notice string, ok bool) {
	chat := m.Chat.ID
	shop := b.d.Backend.Shop()
	t, open := shop.TopupDraft(ctx, chat)
	if !open {
		return "", false
	}
	if t.Amount == 0 {
		amount, valid := parseAmount(m.Text)
		if !valid {
			return "", false
		}
		if _, err := shop.SetTopupAmount(ctx, chat, amount); err != nil {
			return w.topupError(err), true
		}
		b.freshScreen(chat, "bt", "")
		return "", true
	}
	photo := m.Picture()
	if photo == "" {
		return "", false
	}
	if _, err := shop.SubmitTopup(ctx, chat, photo); err != nil {
		return w.topupError(err), true
	}
	return fmt.Sprintf(w.topupSent, w.price(t.Amount, t.Currency)), true
}

// parseAmount reads "500", "500.50" or "500,5" as minor units.
func parseAmount(s string) (int64, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	s = strings.ReplaceAll(s, ",", ".")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(whole) > 9 || len(frac) > 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	var k int64
	if frac != "" {
		if len(frac) == 1 {
			frac += "0"
		}
		if k, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, false
		}
	}
	return n*100 + k, n*100+k > 0
}

// freshScreen sends screen as a new menu message, the previous one removed.
func (b *Bot) freshScreen(chat int64, screen, notice string) {
	b.d.Outbox.Reply(chat, "menu:"+strconv.FormatInt(chat, 10), 2, func(ctx context.Context, c *tgapi.Client) error {
		text, kb := b.screen(ctx, b.config(ctx), chat, screen, notice)
		sent, err := c.Send(ctx, chat, text, kb, false)
		if err != nil {
			return err
		}
		if old, err := b.d.Backend.Chat(ctx, chat); err == nil && old.MenuMsgID != 0 && old.MenuMsgID != sent.MessageID {
			_ = c.Delete(ctx, chat, old.MenuMsgID)
		}
		_ = b.d.Backend.SetMenu(ctx, chat, sent.MessageID)
		return nil
	})
}

// topupDecided is what balance.topup says: the account learns its top-up went on the
// balance, or why not.
func (b *Bot) topupDecided(ctx context.Context, chat int64, t events.Topup) {
	w := wordsFor(b.lang(ctx))
	text := fmt.Sprintf(w.topupApproved, w.price(t.Amount, t.Currency), w.price(t.Balance, t.Currency))
	if t.Status != model.TopupApproved {
		text = fmt.Sprintf(w.topupRejected, w.price(t.Amount, t.Currency))
		if t.Reason != "" {
			text += "\n" + t.Reason
		}
	}
	b.freshMenu(chat, text)
}

// balancePaidScreen is the screen of a payment the balance paid at once: payment.paid
// follows with what it gave.
func (w *words) balancePaidScreen(amount int64, currency string) (string, *tgapi.Keyboard) {
	return html.EscapeString(fmt.Sprintf(w.balancePaid, w.price(amount, currency))), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.back, CallbackData: "m"}}}}
}

// topupError is what the account is told for a refusal of a top-up (model.Refusal).
func (w *words) topupError(err error) string {
	code, _ := model.Refusal(err)
	switch code {
	case model.ErrTopupAmount.Error():
		return w.topupBad
	case model.ErrTopupMany.Error():
		return w.topupMany
	case model.ErrBalanceOff.Error():
		return w.balanceOff
	case model.ErrTopupNone.Error():
		return w.topupNone
	}
	return w.payUnavailable
}

func (w *words) topupBounds(c model.BalanceConfig) string {
	switch {
	case c.Min > 0 && c.Max > 0:
		return fmt.Sprintf(w.topupFromTo, w.price(c.Min, c.Currency), w.price(c.Max, c.Currency))
	case c.Min > 0:
		return fmt.Sprintf(w.topupFrom, w.price(c.Min, c.Currency))
	}
	return fmt.Sprintf(w.topupTo, w.price(c.Max, c.Currency))
}

func (w *words) balanceOp(kind string) string {
	switch kind {
	case model.OpTopup:
		return w.opTopup
	case model.OpSpend:
		return w.opSpend
	case model.OpRefund:
		return w.opRefund
	}
	return w.opAdjust
}
