package tgbot

import (
	"errors"
	"strings"
	"testing"

	"mikan/internal/tgapi"
)

// The channel's and the free plan's texts are there in both languages, without a dash.
func TestChannelTexts(t *testing.T) {
	for name, w := range map[string]*words{"ru": &ru, "en": &en} {
		for field, s := range map[string]string{"channel": w.channel, "freeButton": w.freeButton, "channelJoin": w.channelJoin, "channelCheck": w.channelCheck,
			"channelNotYet": w.channelNotYet, "freeDone": w.freeDone, "freeActive": w.freeActive, "freeOff": w.freeOff, "freeFail": w.freeFail, "freeChannel": w.freeChannel} {
			if s == "" || strings.ContainsAny(s, "—–") {
				t.Errorf("%s.%s is empty or has a dash: %q", name, field, s)
			}
		}
	}
}

// The channel is one Telegram can be asked about and joined.
func TestChannelValidate(t *testing.T) {
	for _, c := range []struct {
		ch   tgapi.Channel
		want error
	}{
		{tgapi.Channel{}, nil},
		{tgapi.Channel{Required: true}, tgapi.ErrChannel},
		{tgapi.Channel{Chat: "@abc"}, tgapi.ErrChannel},
		{tgapi.Channel{Chat: "mikan_news"}, tgapi.ErrChannel},
		{tgapi.Channel{Required: true, Chat: " @mikan_news "}, nil},
		{tgapi.Channel{Chat: "-1001234567890"}, tgapi.ErrChannelURL},
		{tgapi.Channel{Chat: "-1001234567890", URL: "https://t.me/+abcdef"}, nil},
		{tgapi.Channel{Chat: "@mikan_news", URL: "http://t.me/mikan_news"}, tgapi.ErrChannelURL},
	} {
		cfg := Default("ru")
		cfg.Channel = c.ch
		if err := cfg.Validate(); !errors.Is(err, c.want) {
			t.Errorf("%+v: %v, want %v", c.ch, err, c.want)
		}
	}
	cfg := Default("ru")
	cfg.Texts.FreeButton = strings.Repeat("я", 41)
	if err := cfg.Validate(); !errors.Is(err, ErrButtonLabel) {
		t.Fatalf("a long free plan button: %v", err)
	}
}
