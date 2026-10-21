package tgapi

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// The channel a subscriber must join before the bot shows its menu, and the one the free
// plan may ask for (billing.FreeChannel). The gateway checks who is in it; the bot's
// screens decide what to show.

// Channel is the bot's channel and whether joining it is required. The bot checks members
// with getChatMember, which in a channel only its admins may call.
type Channel struct {
	Required bool   `json:"required" doc:"Обязательная подписка: без неё бот показывает вместо меню экран с кнопкой канала"`
	Chat     string `json:"chat" maxLength:"64" doc:"Публичный @username канала или его числовой ID (-100…); бот должен быть администратором канала"`
	URL      string `json:"url,omitempty" maxLength:"256" doc:"Ссылка, по которой вступают (https://t.me/…); пусто: t.me/<username>. Для канала по числовому ID обязательна"`
}

// Link is where a subscriber joins the channel: the admin's link, or the public channel's
// own; "" when there is neither.
func (ch Channel) Link() string {
	if ch.URL != "" {
		return ch.URL
	}
	if name, ok := strings.CutPrefix(ch.Chat, "@"); ok {
		return "https://t.me/" + name
	}
	return ""
}

// Gate: the channel is required and set up. A switch without a channel locks nobody out.
func (ch Channel) Gate() bool { return ch.Required && ch.Chat != "" }

// channelChat is a channel as Bot API methods take it: a public @username (5 to 32 letters,
// digits and underscores, as Telegram allows) or a numeric chat id.
var channelChat = regexp.MustCompile(`^(@[A-Za-z0-9_]{5,32}|-?[0-9]{1,20})$`)

// Validation codes (the UI translates tg_<code>).
var (
	ErrChannel    = errors.New("tg_channel")
	ErrChannelURL = errors.New("tg_channel_url")
)

// Validate trims the channel and checks it can be asked about and joined: a channel by
// its numeric id has no public link, so the admin's link is required for it.
func (ch *Channel) Validate() error {
	ch.Chat, ch.URL = strings.TrimSpace(ch.Chat), strings.TrimSpace(ch.URL)
	switch {
	case ch.Chat == "" && ch.Required:
		return ErrChannel
	case ch.Chat != "" && !channelChat.MatchString(ch.Chat):
		return ErrChannel
	case ch.URL != "" && !SafeURL(ch.URL):
		return ErrChannelURL
	case ch.Chat != "" && ch.Link() == "":
		return ErrChannelURL
	}
	return nil
}

// SafeURL: links the bot may show — web pages and Telegram links.
func SafeURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" && u.Host != "" || u.Scheme == "tg" && u.Host != "") && u.User == nil
}
