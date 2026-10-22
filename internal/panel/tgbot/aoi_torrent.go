package tgbot

import (
	"context"
	"encoding/json"
	"fmt"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
	"mikan/internal/textutil"
	"mikan/internal/tgapi"
)

// AoiVPN fork: the torrent blocker's notice. The nodes turned the subscriber away for a few
// minutes; the message says why, until when, and that AoiVPN sends torrents past the VPN by
// itself (the blocker only watches apps without the subscription's rules).

func init() { EventTypes = append(EventTypes, events.TorrentBanned) }

func (b *Bot) aoiTorrentBanned(ctx context.Context, e events.Event) error {
	if e.TgID == 0 {
		return nil
	}
	var t events.TorrentBan
	if err := json.Unmarshal(e.Data, &t); err != nil || t.Until == 0 {
		return nil
	}
	u, err := b.d.Backend.User(ctx, e.UserID)
	if err != nil {
		if gone(err) {
			return nil
		}
		return err
	}
	cfg := b.config(ctx)
	w := wordsFor(cfg.Lang)
	text := torrentNotice(w, u, t)
	kb := torrentSupportKeyboard(w, b.supportURL(ctx))
	silent, chat := cfg.QuietNight && night(b.d.Now()), e.TgID
	b.d.Outbox.Notice(chat, func(ctx context.Context, c *tgapi.Client) error {
		// Без «Открыть в AoiVPN»: сообщение получают клиенты без правил (v2rayNG, Happ) —
		// AoiVPN у них ещё нет; ссылки на скачивание — в тексте. Кнопка — только поддержка.
		_, err := c.Send(ctx, chat, text, kb, silent)
		return err
	}, nil)
	return nil
}

func torrentNotice(w *words, u model.User, t events.TorrentBan) string {
	until := model.Time(t.Until).In(textutil.Zone).Format("15:04")
	var text string
	if w == &en {
		text = fmt.Sprintf("⚠️ <b>Torrent traffic noticed</b>\n\n"+
			"Your subscription is paused for %d min, until %s MSK: torrents are not allowed through our servers.\n\n"+
			"In <b>AoiVPN</b> torrents go past the VPN by themselves — install it so this does not happen again "+
			"(<a href=\"https://api.aoimusic.ru/api/version/download?platform=aoivpn\">Android</a> · "+
			"<a href=\"https://api.aoimusic.ru/api/version/download?platform=aoivpn&amp;os=windows\">Windows</a>).\n\n"+
			"Not downloading torrents? Look for an app using P2P/DHT: torrent clients, some players and game launchers.",
			t.Minutes, until)
	} else {
		text = fmt.Sprintf("⚠️ <b>Замечен торрент-трафик</b>\n\n"+
			"Ваша подписка приостановлена на %d мин, до %s МСК: качать торренты через наши серверы нельзя.\n\n"+
			"В <b>AoiVPN</b> торренты сами идут мимо VPN — установите его, чтобы такого больше не было "+
			"(<a href=\"https://api.aoimusic.ru/api/version/download?platform=aoivpn\">Android</a> · "+
			"<a href=\"https://api.aoimusic.ru/api/version/download?platform=aoivpn&amp;os=windows\">Windows</a>).\n\n"+
			"Не качали торренты? Проверьте приложения с P2P/DHT: торрент-клиенты, некоторые плееры и лаунчеры игр.",
			t.Minutes, until)
	}
	return text
}

// torrentSupportKeyboard: «это ошибка» ведёт в поддержку панели (её настройка); без неё — без кнопок.
func torrentSupportKeyboard(w *words, support string) *tgapi.Keyboard {
	if support == "" {
		return nil
	}
	label := "💬 Это ошибка — написать в поддержку"
	if w == &en {
		label = "💬 A mistake? Contact support"
	}
	return &tgapi.Keyboard{InlineKeyboard: [][]tgapi.Button{{{Text: label, URL: support}}}}
}
