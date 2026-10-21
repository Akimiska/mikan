package tgbot

import (
	"context"
	"fmt"
	"html"
	"net/url"

	"mikan/internal/panel/tgate"
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

func (b *Bot) referralScreen(ctx context.Context, w *words, chat int64, notice string) (string, *tgate.Keyboard) {
	rw := refWordsFor(w)
	back := []tgate.Button{{Text: w.back, CallbackData: "m"}}
	st, err := b.d.Backend.Referral(ctx, chat)
	if err != nil || st.Link == "" {
		if err != nil {
			b.d.Log.Warn("telegram: referral screen", "err", err)
		}
		return rw.off, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{back}}
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
	return text, &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: rw.share, URL: share}}, back}}
}
