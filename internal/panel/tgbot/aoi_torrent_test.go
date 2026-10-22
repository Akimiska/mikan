package tgbot

import (
	"strings"
	"testing"

	"mikan/internal/panel/events"
	"mikan/internal/panel/model"
)

func TestTorrentNotice(t *testing.T) {
	u := model.User{Name: "Setsuna6666 (317032558)"}
	text, kb := torrentNotice(&ru, u, events.TorrentBan{Until: 1792681107, Minutes: 15}, "https://sub.example/sub/abc")
	for _, want := range []string{"«Setsuna6666»", "15 мин", "до 17:58 МСК", "AoiVPN"} {
		if !strings.Contains(text, want) {
			t.Errorf("notice lacks %q:\n%s", want, text)
		}
	}
	if kb == nil || len(kb.InlineKeyboard) != 1 || !strings.HasSuffix(kb.InlineKeyboard[0][0].URL, "#open=AoiVPN") {
		t.Errorf("keyboard = %+v, want the open-in-AoiVPN button", kb)
	}
}
