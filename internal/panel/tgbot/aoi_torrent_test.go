package tgbot

import (
	"strings"
	"testing"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
)

func TestTorrentNotice(t *testing.T) {
	u := model.User{Name: "Setsuna6666 (317032558)"}
	text := torrentNotice(&ru, u, events.TorrentBan{Until: 1792681107, Minutes: 15})
	for _, want := range []string{"Ваша подписка приостановлена", "15 мин", "до 17:58 МСК", "AoiVPN"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice lacks %q:\n%s", want, text)
		}
	}
}

func TestTorrentSupportKeyboard(t *testing.T) {
	if kb := torrentSupportKeyboard(&ru, ""); kb != nil {
		t.Errorf("no support link must give no keyboard, got %+v", kb)
	}
	kb := torrentSupportKeyboard(&ru, "https://t.me/aoisupport")
	if kb == nil || kb.InlineKeyboard[0][0].URL != "https://t.me/aoisupport" {
		t.Errorf("keyboard = %+v, want the support button", kb)
	}
}
