package tgbot

import (
	"context"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"

	"mikan/internal/panel/model"
	"mikan/internal/tgapi"
)

// AoiVPN fork: the shop for a single paid tariff. The upstream screens are made for many
// tariffs ("Стандарт — 30 дн. – 180 дн."); with one, "Продлить" and "Купить" show its terms
// at once, named in months with the discount of the longer ones against the monthly price.

// aoiSingle is the one tariff on sale (its id, as the shop's callback data takes it).
func (b *Bot) aoiSingle(ctx context.Context) (string, bool) {
	offers, _ := b.offers(ctx)
	if len(offers) != 1 || len(offers[0].Terms) < 2 {
		return "", false
	}
	return strconv.FormatInt(offers[0].Tariff.ID, 10), true
}

// aoiBack: with one tariff, its term list is the shop's first screen, so back from it goes
// home instead of to the list of tariffs that is never shown.
func (b *Bot) aoiBack(ctx context.Context, w *words, arg string, def []tgapi.Button) []tgapi.Button {
	if _, ok := b.aoiSingle(ctx); ok && !strings.Contains(arg, ".") {
		return []tgapi.Button{{Text: w.back, CallbackData: "m"}}
	}
	return def
}

// aoiMonthly is the price of the tariff's 30-day term, in kopecks; 0 without one.
func aoiMonthly(o model.Offer) int64 {
	for _, t := range o.Terms {
		if t.Days == 30 {
			return t.Rub
		}
	}
	return 0
}

// aoiDiscount is how much cheaper a term is than as many months bought one by one, in %.
func aoiDiscount(o model.Offer, t model.OfferTerm) int64 {
	monthly := aoiMonthly(o)
	if monthly <= 0 || t.Days%30 != 0 || t.Rub <= 0 {
		return 0
	}
	full := monthly * (t.Days / 30)
	if full <= t.Rub {
		return 0
	}
	return int64(math.Round(float64(full-t.Rub) * 100 / float64(full)))
}

func aoiMonths(n int64, en bool) string {
	if en {
		if n == 1 {
			return "1 month"
		}
		return strconv.FormatInt(n, 10) + " months"
	}
	switch {
	case n%10 == 1 && n%100 != 11:
		return strconv.FormatInt(n, 10) + " месяц"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return strconv.FormatInt(n, 10) + " месяца"
	}
	return strconv.FormatInt(n, 10) + " месяцев"
}

// aoiPrice is "456 ₽ (−5%)".
func aoiPrice(w *words, o model.Offer, t model.OfferTerm) string {
	p := w.cheapest(model.Offer{Stars: t.Stars, Rub: t.Rub})
	if d := aoiDiscount(o, t); d > 0 {
		p += fmt.Sprintf(" (−%d%%)", d)
	}
	return p
}

// aoiTermLabel is a term's button: "3 месяца · 456 ₽ (−5%)"; false for a term that is not
// whole months.
func aoiTermLabel(o model.Offer, t model.OfferTerm, w *words) (string, bool) {
	if t.Days <= 0 || t.Days%30 != 0 || t.Rub <= 0 {
		return "", false
	}
	return aoiMonths(t.Days/30, w == &en) + " · " + aoiPrice(w, o, t), true
}

// aoiPickText heads the term list of the single tariff: what is bought, what it gives (the
// tariff's description, the admin's words) and that longer is cheaper.
func (b *Bot) aoiPickText(ctx context.Context, w *words, o model.Offer, renewing *model.User) (string, bool) {
	if _, ok := b.aoiSingle(ctx); !ok {
		return "", false
	}
	en := w == &en
	title, hint := "Новая подписка", "Выберите срок — чем дольше, тем выгоднее:"
	if en {
		title, hint = "New subscription", "Pick a term — the longer, the cheaper:"
	}
	lines := []string{}
	if renewing != nil {
		title = "Продление подписки"
		if en {
			title = "Renew the subscription"
		}
		lines = append(lines, "<b>"+title+"</b>")
		if renewing.ExpiresAt.Valid {
			now := "Сейчас действует до %s."
			if en {
				now = "Valid now until %s."
			}
			lines = append(lines, fmt.Sprintf(now, html.EscapeString(w.date(model.Time(renewing.ExpiresAt.Int64)))))
		}
	} else {
		lines = append(lines, "<b>"+title+"</b>")
	}
	if d := strings.TrimSpace(o.Tariff.Description); d != "" {
		lines = append(lines, html.EscapeString(d))
	}
	lines = append(lines, "", hint)
	for _, t := range o.Terms {
		if l, ok := aoiTermLabel(o, t, w); ok {
			lines = append(lines, "• "+html.EscapeString(strings.Replace(l, " · ", " — ", 1)))
		}
	}
	return strings.Join(lines, "\n"), true
}

// aoiTermText heads the chosen term's payment screen: "Продление на 3 месяца — 456 ₽ (−5%)".
func (b *Bot) aoiTermText(ctx context.Context, w *words, o model.Offer, t model.OfferTerm, renewing *model.User) (string, bool) {
	if _, ok := b.aoiSingle(ctx); !ok || t.Days%30 != 0 {
		return "", false
	}
	en := w == &en
	what := "Новая подписка на"
	if renewing != nil {
		what = "Продление на"
	}
	if en {
		what = "New subscription for"
		if renewing != nil {
			what = "Renewal for"
		}
	}
	return "<b>" + what + " " + aoiMonths(t.Days/30, en) + "</b> — " + html.EscapeString(aoiPrice(w, o, t)), true
}

// aoiPageRows (AoiVPN fork) are the buttons of the admin's page «О сервисе» (id "about"), as
// the legacy bot had them: the channel, the documents, the price list and support.
func aoiPageRows(id string, w *words, support string) [][]tgapi.Button {
	if id != "about" {
		return nil
	}
	const docs = "https://sub.aoimusic.ru/vpn/"
	l := map[bool][5]string{
		false: {"📢 Телеграм-канал", "🔒 Политика конфиденциальности", "📜 Пользовательское соглашение", "💰 Тарифы", "💬 Поддержка"},
		true:  {"📢 Telegram channel", "🔒 Privacy policy", "📜 Terms of service", "💰 Pricing", "💬 Support"},
	}[w == &en]
	rows := [][]tgapi.Button{
		{{Text: l[0], URL: "https://t.me/AoiVPN"}},
		{{Text: l[1], URL: docs + "privacy.html"}},
		{{Text: l[2], URL: docs + "terms.html"}},
		{{Text: l[3], URL: docs + "pricing.html"}},
	}
	if support != "" {
		rows = append(rows, []tgapi.Button{{Text: l[4], URL: support}})
	}
	return rows
}
