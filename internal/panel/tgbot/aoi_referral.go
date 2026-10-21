package tgbot

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mikan/internal/panel/model"
	"mikan/internal/textutil"
	"mikan/internal/tgapi"
)

// AoiVPN fork: the «Пригласить друга» screen of the referral programme (aoiref). Its words
// live here, not in texts.go, so a merge of upstream's texts never meets them.

type refWords struct {
	title, intro, stats, bank, share, shareText, invalid, off string
}

var refRu = refWords{
	title:     "👥 Пригласить друга",
	intro:     "Друг по вашей ссылке получает пробный период. Когда он оплатит подписку, вам — <b>+7 дней</b> (начисляются через сутки после приглашения).",
	stats:     "Приглашено: <b>%d</b> · оплатили: <b>%d</b> · бонусов: <b>%d</b> (+%d дн.)",
	bank:      "Ждут подписки: <b>%d дн.</b> — добавятся, как только она появится.",
	share:     "📤 Поделиться ссылкой",
	shareText: "Попробуй AoiVPN бесплатно: быстрое подключение и безопасный доступ.",
	invalid:   "Реферальная ссылка недействительна.",
	off:       "Приглашения сейчас недоступны.",
}

var refEn = refWords{
	title:     "👥 Invite a friend",
	intro:     "A friend who joins by your link gets the trial. Once they pay for a subscription, you get <b>+7 days</b> (given a day after the invite).",
	stats:     "Invited: <b>%d</b> · paid: <b>%d</b> · rewards: <b>%d</b> (+%d d)",
	bank:      "Waiting for a subscription: <b>%d d</b> — added as soon as you have one.",
	share:     "📤 Share the link",
	shareText: "Try AoiVPN for free: quick to connect, safe to use.",
	invalid:   "This invite link is not valid.",
	off:       "Invites are not available now.",
}

func refWordsFor(w *words) *refWords {
	if w == &en {
		return &refEn
	}
	return &refRu
}

func (b *Bot) referralScreen(ctx context.Context, w *words, chat int64, notice string) (string, *tgapi.Keyboard) {
	rw := refWordsFor(w)
	back := []tgapi.Button{{Text: w.back, CallbackData: "m"}}
	st, err := b.d.Backend.Referral(ctx, chat)
	if err != nil || st.Link == "" {
		if err != nil {
			b.d.Log.Warn("telegram: referral screen", "err", err)
		}
		return rw.off, &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{back}}
	}
	text := "<b>" + rw.title + "</b>\n\n" + rw.intro + "\n\n<code>" + html.EscapeString(st.Link) + "</code>\n\n" +
		fmt.Sprintf(rw.stats, st.Invited, st.Paid, st.Rewarded, st.Days)
	if st.Bank > 0 {
		text += "\n" + fmt.Sprintf(rw.bank, st.Bank)
	}
	if notice != "" {
		text = html.EscapeString(notice) + "\n\n" + text
	}
	share := "https://t.me/share/url?url=" + url.QueryEscape(st.Link) + "&text=" + url.QueryEscape(rw.shareText)
	return text, &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: rw.share, URL: share}}, back}}
}

// subName (AoiVPN fork) is a subscription's name as the subscriber reads it: the panel names
// them "<who> (<telegram id>)" for the admin, the id is noise in the bot.
func subName(u model.User) string {
	n := strings.TrimSpace(u.Name)
	if i := strings.LastIndex(n, " ("); i > 0 && strings.HasSuffix(n, ")") {
		if _, err := strconv.ParseInt(n[i+2:len(n)-1], 10, 64); err == nil {
			n = n[:i]
		}
	}
	return n
}

// aoiConnectRows (AoiVPN fork) are the connect screen's buttons: the subscription opened in
// AoiVPN (or Happ) at once, then the AoiVPN downloads, always the latest release (the Aoi
// backend redirects to the file). Telegram buttons open web links only, so "open" goes
// through the subscription page: its #open=<app> starts the app with the subscription.
func aoiConnectRows(w *words, subURL string) [][]tgapi.Button {
	open, happ, android, windows := "⚡ Открыть в AoiVPN", "🍏 Открыть в Happ", "📥 AoiVPN для Android", "📥 AoiVPN для Windows"
	if w == &en {
		open, happ, android, windows = "⚡ Open in AoiVPN", "🍏 Open in Happ", "📥 AoiVPN for Android", "📥 AoiVPN for Windows"
	}
	const base = "https://api.aoimusic.ru/api/version/download?platform=aoivpn"
	var rows [][]tgapi.Button
	if subURL != "" {
		rows = append(rows, []tgapi.Button{{Text: open, URL: subURL + "#open=AoiVPN"}})
	}
	rows = append(rows, []tgapi.Button{{Text: android, URL: base}, {Text: windows, URL: base + "&os=windows"}})
	if subURL != "" {
		rows = append(rows, []tgapi.Button{{Text: happ, URL: subURL + "#open=Happ"}})
	}
	return rows
}

// aoiVars (AoiVPN fork) adds what the legacy bot's cabinet showed: {until_full} is the end of
// the term with its time, "16 декабря 2026, 21:00 МСК"; {term_line} is the term's line as the
// subscription stands: valid until, over since (a past date with "0 days left" says nothing
// right), or without an end.
func aoiVars(v map[string]string, w *words, u model.User, now time.Time) {
	en := w == &en
	v["until_full"] = v["until"]
	v["term_line"] = "📅 Бессрочно"
	if en {
		v["term_line"] = "📅 No end date"
	}
	if !u.ExpiresAt.Valid {
		return
	}
	exp := model.Time(u.ExpiresAt.Int64)
	t := exp.In(textutil.Zone)
	zone := "МСК"
	if en {
		zone = "MSK"
	}
	v["until_full"] = w.date(t) + ", " + t.Format("15:04") + " " + zone
	switch {
	case !now.Before(exp) && en:
		v["term_line"] = "📅 Ended on " + w.date(t) + " — renew with «💳 Renew»"
	case !now.Before(exp):
		v["term_line"] = "📅 Закончилась " + w.date(t) + " — продлите в «💳 Продлить»"
	case en:
		v["term_line"] = "📅 Valid until " + v["until_full"] + " (" + v["days"] + " left)"
	default:
		v["term_line"] = "📅 Действует до: " + v["until_full"] + " (осталось " + v["days"] + ")"
	}
}
