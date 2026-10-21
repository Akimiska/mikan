package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
	"mikan/internal/tgapi"
)

// Callback data of the shop: b buy a new subscription, tn:<tariff> its tariff, pn:<tariff>:<p>
// pay for it; r renew the shown one, t:<tariff>, py:<tariff>:<p>. <tariff> is the tariff's
// id, or <id>.<days> for one of its terms (a tariff sold for several); the id alone is its
// first term, as on buttons sent before terms. <p> is s (Stars) or a-<id> (a marketplace
// adapter); y and c are YooKassa's and CryptoBot's, short, and as messages sent before
// 0.4.4 carry them for the built-in providers those adapters replaced. The longest,
// py:<19 digits>.<4 digits>:a-<32>, is 62 bytes: Telegram takes 64.

var providerCodes = map[string]string{"s": model.Stars, "b": model.Balance, "y": model.AddonPrefix + "yookassa", "c": model.AddonPrefix + "cryptobot"}

// addonCode is an adapter's code on a pay button.
func addonCode(id string) string {
	for code, p := range providerCodes {
		if p == model.AddonPrefix+id {
			return code
		}
	}
	return "a-" + id
}

// providerOf is the provider a button's code names.
func providerOf(code string) (string, bool) {
	if p, ok := providerCodes[code]; ok {
		return p, true
	}
	id, ok := strings.CutPrefix(code, "a-")
	if !ok || model.AddonID(model.AddonPrefix+id) == "" {
		return "", false
	}
	return model.AddonPrefix + id, true
}

// addonName names adapters on the buttons in the bot's language.
func (b *Bot) addonName(ctx context.Context) func(id string) string {
	lang := b.lang(ctx)
	shop := b.d.Backend.Shop()
	return func(id string) string { return shop.AddonName(ctx, id, lang) }
}

// offersMemo keeps the shop's offers for one update (Update puts it in the context): one tap
// draws a screen that asks for them more than once (canBuyNew, the menu, the list), and in
// the addon's mode each ask is a call to the panel. The next update asks again, so a changed
// tariff shows at once.
type offersMemo struct {
	mu   sync.Mutex
	done bool
	of   []model.Offer
	av   model.Available
}

type offersKey struct{}

func withOffersMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, offersKey{}, &offersMemo{})
}

// offers are the tariffs on sale and the ways to pay now, asked once an update; a failed
// ask is not kept.
func (b *Bot) offers(ctx context.Context) ([]model.Offer, model.Available) {
	m, _ := ctx.Value(offersKey{}).(*offersMemo)
	if m == nil {
		m = &offersMemo{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.done {
		return m.of, m.av
	}
	o, av, err := b.d.Backend.Shop().Offers(ctx)
	if err != nil {
		b.d.Log.Warn("telegram: offers", "err", err)
		return nil, av
	}
	m.done, m.of, m.av = true, o, av
	return o, av
}

// canBuyNew: people without a subscription may buy one here.
func (b *Bot) canBuyNew(ctx context.Context) bool {
	if !b.d.Backend.Shop().Config(ctx).AllowNew {
		return false
	}
	o, _ := b.offers(ctx)
	return len(o) > 0
}

// price: "⭐ 150" or "199 ₽" / "199,50 ₽".
func (w *words) price(amount int64, currency string) string {
	if currency == "XTR" {
		return "⭐ " + strconv.FormatInt(amount, 10)
	}
	sign, ok := currencySigns[currency]
	if !ok {
		sign = currency
	}
	s := strconv.FormatInt(amount/100, 10)
	if k := amount % 100; k != 0 {
		sep := ","
		if w == &en {
			sep = "."
		}
		s += fmt.Sprintf("%s%02d", sep, k)
	}
	return s + " " + sign
}

// currencySigns: how amounts of a balance's currency show; another shows by its code.
var currencySigns = map[string]string{"RUB": "₽", "USD": "$", "EUR": "€", "UAH": "₴", "BYN": "Br", "KZT": "₸", "UZS": "сўм", "USDT": "USDT"}

// maxMessage is what one message of the bot holds: Telegram takes 4096 characters of the text
// it shows, and the markup counted here as well leaves room.
const maxMessage = 4000

// shopList: the tariffs on sale as buttons; prefix is "t" (renew) or "tn" (new). A list
// too long for one message drops what each tariff is made of (its own screen shows it),
// then the lines that do not fit: the buttons still lead to every tariff.
func (b *Bot) shopList(ctx context.Context, w *words, title, prefix, notice string, back []tgapi.Button) (string, *tgapi.Keyboard) {
	offers, _ := b.offers(ctx)
	lang := b.lang(ctx)
	rows := [][]tgapi.Button{}
	for _, o := range offers {
		rows = append(rows, []tgapi.Button{{Text: o.Tariff.Name + " · " + w.cheapest(o), CallbackData: prefix + ":" + strconv.FormatInt(o.Tariff.ID, 10)}})
	}
	list := func(whole bool) []string {
		lines := []string{"<b>" + html.EscapeString(title) + "</b>", ""}
		if notice != "" {
			lines = append([]string{html.EscapeString(notice), ""}, lines...)
		}
		for _, o := range offers {
			line := "• <b>" + html.EscapeString(o.Tariff.Name) + "</b> — " + html.EscapeString(model.DescribeOffer(o, lang))
			if whole {
				line += contentsOf(o, lang) + noteOf(o.Tariff.Description)
			}
			lines = append(lines, line)
		}
		if len(offers) == 0 {
			lines = append(lines, html.EscapeString(w.payUnavailable))
		}
		return lines
	}
	lines := list(true)
	if utf8.RuneCountInString(strings.Join(lines, "\n")) > maxMessage {
		lines = list(false)
	}
	for len(lines) > 1 && utf8.RuneCountInString(strings.Join(lines, "\n")) > maxMessage {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n"), &tgapi.Keyboard{InlineKeyboard: append(rows, back)}
}

// contentsOf is what a tariff is made of besides its limits (model.DescribeContents), a
// line each after the line of its limits; empty for a tariff without groups or gifts.
func contentsOf(o model.Offer, lang string) string {
	var b strings.Builder
	for _, l := range model.DescribeContents(o, lang) {
		b.WriteString("\n   " + html.EscapeString(l))
	}
	return b.String()
}

// noteOf is the admin's description of a tariff to follow the line built from its limits:
// on lines of its own, escaped for the HTML parse mode, its line breaks kept. Empty without
// a description, so a tariff that has none looks as it did.
func noteOf(description string) string {
	if description == "" {
		return ""
	}
	return "\n" + html.EscapeString(description)
}

// cheapest is the price a list shows: rubles when sold for them, else Stars; "from" the
// cheapest term of a tariff sold for several.
func (w *words) cheapest(o model.Offer) string {
	terms := o.Terms
	if len(terms) == 0 {
		terms = []model.OfferTerm{{Stars: o.Stars, Rub: o.Rub}}
	}
	var rub, stars int64
	for _, t := range terms {
		if t.Rub > 0 && (rub == 0 || t.Rub < rub) {
			rub = t.Rub
		}
		if t.Stars > 0 && (stars == 0 || t.Stars < stars) {
			stars = t.Stars
		}
	}
	price := w.price(stars, "XTR")
	if rub > 0 {
		price = w.price(rub, "RUB")
	}
	if len(terms) > 1 {
		return fmt.Sprintf(w.priceFrom, price)
	}
	return price
}

// tariffArg reads <id> or <id>.<days> from callback data; days is nil without them.
func tariffArg(s string) (id int64, days *int64) {
	idStr, daysStr, found := strings.Cut(s, ".")
	id, _ = strconv.ParseInt(idStr, 10, 64)
	if found {
		if d, err := strconv.ParseInt(daysStr, 10, 64); err == nil && d >= 0 {
			days = &d
		}
	}
	return id, days
}

// shopTariff: one tariff and a button per way to pay. A tariff sold for several terms
// shows its terms first, a button each. arg is <id> or <id>.<days>; prefix is the screen's
// ("tn" new, "t" renew), pay the pay buttons' ("pn", "py").
func (b *Bot) shopTariff(ctx context.Context, w *words, arg, prefix, pay string, renewing *model.User, back []tgapi.Button) (string, *tgapi.Keyboard) {
	id, days := tariffArg(arg)
	offers, av := b.offers(ctx)
	lang := b.lang(ctx)
	for _, o := range offers {
		if o.Tariff.ID != id {
			continue
		}
		name := "<b>" + html.EscapeString(o.Tariff.Name) + "</b>\n"
		ref := strconv.FormatInt(id, 10)
		if days == nil && len(o.Terms) == 1 && o.Terms[0].Days != o.First {
			// The first term is not sold now (no provider takes its price): the buttons name
			// the one that is, as an invoice without days would be for the first.
			only := o.Terms[0].Days
			days = &only
		}
		if days == nil && len(o.Terms) > 1 {
			rows := [][]tgapi.Button{}
			for _, t := range o.Terms {
				label := model.TermLabel(o.Tariff, t.Days, lang) + " · " + w.cheapest(model.Offer{Stars: t.Stars, Rub: t.Rub})
				if l, ok := aoiTermLabel(o, t, w); ok { // AoiVPN fork: months, the discount
					label = l
				}
				rows = append(rows, []tgapi.Button{{Text: label, CallbackData: prefix + ":" + ref + "." + strconv.FormatInt(t.Days, 10)}})
			}
			if text, ok := b.aoiPickText(ctx, w, o, renewing); ok { // AoiVPN fork
				return text, &tgapi.Keyboard{InlineKeyboard: append(rows, back)}
			}
			return name + html.EscapeString(model.DescribeLimits(o.Tariff, lang)) + contentsOf(o, lang) + noteOf(o.Tariff.Description) + "\n\n" + w.pickTerm, &tgapi.Keyboard{InlineKeyboard: append(rows, back)}
		}
		t, ok := o.Term(days)
		if !ok {
			break
		}
		if days != nil {
			ref += "." + strconv.FormatInt(t.Days, 10)
		}
		text := name + html.EscapeString(model.Describe(o.Tariff, t.Days, lang)) + contentsOf(o, lang) + noteOf(o.Tariff.Description) + "\n\n" + w.payHow
		if head, ok := b.aoiTermText(ctx, w, o, t, renewing); ok { // AoiVPN fork
			text = head + "\n\n" + w.payHow
		}
		stars, rub := t.Stars, t.Rub
		if renewing != nil {
			if renewing.TariffID.Valid && renewing.TariffID.Int64 != o.Tariff.ID {
				// The renewal moves the subscription to this tariff (domain.carryOver).
				text += "\n<i>" + html.EscapeString(w.carryOver) + "</i>"
			}
			// The devices bought that stay with renewals, as Invoice adds them.
			moreStars, moreRub := model.RenewalExtras(b.d.Backend.Shop().Config(ctx), *renewing, o.Tariff)
			if stars > 0 {
				stars += moreStars
			}
			if rub > 0 {
				rub += moreRub
			}
		}
		rows := w.payButtons(pay+":"+ref+":", stars, rub, av, b.addonName(ctx))
		return text, &tgapi.Keyboard{InlineKeyboard: append(rows, back)}
	}
	return html.EscapeString(w.notForSale), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
}

// shopBack is where the back button of a tariff screen leads: the term list of a tariff
// sold for several when a term is shown, else the list of tariffs.
func shopBack(w *words, prefix, arg, list string) []tgapi.Button {
	if id, _, found := strings.Cut(arg, "."); found {
		return []tgapi.Button{{Text: w.back, CallbackData: prefix + ":" + id}}
	}
	return []tgapi.Button{{Text: w.back, CallbackData: list}}
}

// shopInvoice opens the invoice and shows its pay button. It runs when the chat's screen
// is drawn, off the update loop: the provider may take seconds to answer.
func (b *Bot) shopInvoice(ctx context.Context, w *words, chat, userID int64, arg string, back []tgapi.Button) (string, *tgapi.Keyboard) {
	ref, code, _ := strings.Cut(arg, ":")
	id, days := tariffArg(ref)
	provider, ok := providerOf(code)
	shop := b.d.Backend.Shop()
	if !ok {
		return html.EscapeString(w.payUnavailable), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	p, err := shop.Invoice(ctx, model.InvoiceRequest{TgID: chat, UserID: userID, TariffID: id, TermDays: days, Provider: provider})
	if err != nil {
		return html.EscapeString(w.payError(err)), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	done := w.payNew
	if userID != 0 {
		done = w.payRenew
	}
	name := p.TariffName
	if days != nil && p.TermDays.Valid {
		// A tariff sold for several terms: the invoice says which one.
		if t, err := b.d.Backend.Tariff(ctx, id); err == nil {
			name += " · " + model.TermLabel(t, p.TermDays.Int64, b.lang(ctx))
		}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(name), html.EscapeString(w.price(p.Amount, p.Currency)), done)
	if p.Provider == model.Balance {
		return w.balancePaidScreen(p.Amount, p.Currency)
	}
	return text, &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}

// payError is what the buyer is told for a refusal of a purchase (model.Refusal); what
// has no words of its own here is "unavailable".
func (w *words) payError(err error) string {
	code, _ := model.Refusal(err)
	switch code {
	case model.ErrNotForSale.Error(), model.ErrNewOff.Error():
		return w.notForSale
	case model.ErrTooMany.Error():
		return w.tooManyInvoices
	case model.ErrTooManySubs.Error():
		return fmt.Sprintf(w.linkLimit, model.MaxLinks)
	case model.ErrNotYours.Error():
		return w.noSub
	case model.ErrBalanceLow.Error():
		return w.balanceLow
	case model.ErrBalanceOff.Error():
		return w.balanceOff
	case model.ErrTrialUsed.Error():
		return w.trialUsed
	case model.ErrTrialOff.Error():
		return w.trialOff
	}
	return w.payUnavailable
}

func (b *Bot) lang(ctx context.Context) string { return b.config(ctx).Lang }

// paid is what payment.paid says: the buyer learns the subscription is ready (with the
// link for a new one) or the traffic package is added, and gets a fresh menu that shows it.
func (b *Bot) paid(ctx context.Context, chat int64, p events.Payment, u model.User) {
	w := wordsFor(b.lang(ctx))
	var text string
	switch {
	case p.Kind == model.KindPackage:
		text = fmt.Sprintf(w.paidPackage, html.EscapeString(p.TariffName), html.EscapeString(subName(u)))
	case p.Kind == model.KindDevice:
		text = fmt.Sprintf(w.paidDevice, html.EscapeString(subName(u)), u.DeviceLimit.Int64)
	case p.Created:
		text = fmt.Sprintf(w.paidNew, html.EscapeString(subName(u)), html.EscapeString(p.TariffName), html.EscapeString(b.subURL(ctx, u)))
	default:
		until := w.forever
		if u.ExpiresAt.Valid {
			until = w.date(model.Time(u.ExpiresAt.Int64))
		}
		text = fmt.Sprintf(w.paidRenew, html.EscapeString(subName(u)), html.EscapeString(until))
	}
	_ = b.d.Backend.SetCurrent(ctx, chat, u.ID)
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgapi.Client) error {
		_, err := c.Send(ctx, chat, text, nil, false)
		return err
	}, nil)
	b.freshMenu(chat, "")
}

// refunded is what payment.refunded says: the buyer learns the payment was refunded and what
// it gave is taken back.
func (b *Bot) refunded(ctx context.Context, chat int64, p events.Payment, u model.User) {
	w := wordsFor(b.lang(ctx))
	var text string
	switch {
	case u.ID == 0:
		text = fmt.Sprintf(w.refundedGone, html.EscapeString(p.TariffName))
	case p.Kind == model.KindPackage:
		text = fmt.Sprintf(w.refundedPackage, html.EscapeString(p.TariffName), html.EscapeString(subName(u)))
	case p.Kind == model.KindDevice:
		text = fmt.Sprintf(w.refundedDevice, html.EscapeString(subName(u)), u.DeviceLimit.Int64)
	case p.Disabled:
		text = fmt.Sprintf(w.refundedNew, html.EscapeString(p.TariffName), html.EscapeString(subName(u)))
	default:
		until := w.forever
		if u.ExpiresAt.Valid {
			until = w.date(model.Time(u.ExpiresAt.Int64))
		}
		text = fmt.Sprintf(w.refundedRenew, html.EscapeString(p.TariffName), html.EscapeString(subName(u)), html.EscapeString(until))
	}
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgapi.Client) error {
		_, err := c.Send(ctx, chat, text, nil, false)
		return err
	}, nil)
	b.freshMenu(chat, "")
}

// takeTrial gives the chat its free trial and says what it got, or shows the ways to pay
// for a priced one; a chat that had one (or is a customer) is told why not.
func (b *Bot) takeTrial(ctx context.Context, w *words, chat int64) (string, *tgapi.Keyboard) {
	home := &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.back, CallbackData: "m"}}}}
	shop := b.d.Backend.Shop()
	if shop.Config(ctx).TrialPaid() {
		return b.trialPay(ctx, w, chat)
	}
	u, err := shop.Trial(ctx, chat)
	switch {
	case errors.Is(err, model.ErrTrialUsed):
		return html.EscapeString(w.trialUsed), home
	case errors.Is(err, model.ErrTrialOff):
		return html.EscapeString(w.trialOff), home
	case err != nil:
		b.d.Log.Warn("telegram: trial", "tg", chat, "err", err)
		return html.EscapeString(w.trialFail), home
	}
	what := subName(u)
	if t, err := b.d.Backend.Tariff(ctx, u.TariffID.Int64); err == nil {
		what = model.Describe(t, t.DurationDays, b.lang(ctx))
	}
	return fmt.Sprintf(html.EscapeString(w.trialDone), html.EscapeString(what)), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.trialOpen, CallbackData: "m"}}}}
}

// trialPay shows what the priced trial gives and a button for each way to pay for it.
func (b *Bot) trialPay(ctx context.Context, w *words, chat int64) (string, *tgapi.Keyboard) {
	home := []tgapi.Button{{Text: w.back, CallbackData: "m"}}
	shop := b.d.Backend.Shop()
	o, av, ok := shop.TrialOffer(ctx, chat)
	if !ok {
		why := w.trialOff
		if _, on := shop.TrialTariff(ctx); on {
			// Offered, but not to this account, or for a price nobody takes now.
			why = w.payUnavailable
			if !shop.TrialAllowed(ctx, chat) {
				why = w.trialUsed
			}
		}
		return html.EscapeString(why), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{home}}
	}
	text := "<b>" + html.EscapeString(w.trialTitle) + "</b>\n" + html.EscapeString(model.Describe(o.Tariff, o.Tariff.DurationDays, b.lang(ctx))) + "\n\n" + w.payHow
	rows := w.payButtons("tp:", o.Stars, o.Rub, av, b.addonName(ctx))
	return text, &tgapi.Keyboard{InlineKeyboard: append(rows, home)}
}

// trialInvoice opens the invoice for the priced trial with the provider a button names.
func (b *Bot) trialInvoice(ctx context.Context, w *words, chat int64, code string) (string, *tgapi.Keyboard) {
	back := []tgapi.Button{{Text: w.back, CallbackData: "tr"}}
	provider, ok := providerOf(code)
	shop := b.d.Backend.Shop()
	if !ok {
		return html.EscapeString(w.payUnavailable), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	p, err := shop.TrialInvoice(ctx, chat, provider)
	switch {
	case errors.Is(err, model.ErrTrialUsed):
		return html.EscapeString(w.trialUsed), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.back, CallbackData: "m"}}}}
	case errors.Is(err, model.ErrTrialOff):
		return html.EscapeString(w.trialOff), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.back, CallbackData: "m"}}}}
	case err != nil:
		return html.EscapeString(w.payError(err)), &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(w.trialTitle+" · "+p.TariffName), html.EscapeString(w.price(p.Amount, p.Currency)), w.payNew)
	if p.Provider == model.Balance {
		return w.balancePaidScreen(p.Amount, p.Currency)
	}
	return text, &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}

// trialNotGiven is what trial.not_given says: it tells the buyer a paid trial was not given,
// as the account had its trial, and that its money comes back through support (the payment
// method cannot refund it), with the support link when the panel has one.
func (b *Bot) trialNotGiven(ctx context.Context, chat int64, n events.NotGiven) {
	w := wordsFor(b.lang(ctx))
	text := fmt.Sprintf(w.trialNotGiven, html.EscapeString(w.price(n.Amount, n.Currency)))
	var kb *tgapi.Keyboard
	if sup := b.supportURL(ctx); sup != "" {
		kb = &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: w.support, URL: sup}}}}
	}
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgapi.Client) error {
		_, err := c.Send(ctx, chat, text, kb, false)
		return err
	}, nil)
}
