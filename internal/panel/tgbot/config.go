package tgbot

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Config is what the admin sets up in the panel: the menu, the texts and which
// notifications go out. It is stored as one JSON setting.
type Config struct {
	Lang      string       `json:"lang" enum:"ru,en" doc:"Язык встроенных надписей бота"`
	Buttons   []MenuButton `json:"buttons" doc:"Кнопки главного меню по порядку"`
	Texts     Texts        `json:"texts"`
	Notify    Notify       `json:"notify"`
	MiniApp   bool         `json:"mini_app" doc:"Кнопка Mini App со страницей подписки"`
	CleanChat bool         `json:"clean_chat" doc:"Удалять сообщения пользователя, чтобы в чате было одно меню"`
	// QuietNight: the automatic notices from 22:00 to 9:00 Moscow time come without a sound.
	QuietNight bool `json:"quiet_night" doc:"Уведомления с 22:00 до 9:00 МСК приходят без звука"`
	// PromoButton: «Промокоды» under the menu, opening the Mini App at the promo code field.
	// It needs the Mini App. A bot saved before this option has it on.
	PromoButton bool `json:"promo_button" doc:"Кнопка «Промокоды» в главном меню (нужен Mini App)"`
	// Channel is the channel a subscriber must join before the bot shows its menu, and the
	// one the free plan may ask for (billing.FreeChannel). Off by default.
	Channel Channel `json:"channel"`
}

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

// gate: the channel is required and set up. A switch without a channel locks nobody out.
func (ch Channel) gate() bool { return ch.Required && ch.Chat != "" }

// channelChat is a channel as Bot API methods take it: a public @username (5 to 32 letters,
// digits and underscores, as Telegram allows) or a numeric chat id.
var channelChat = regexp.MustCompile(`^(@[A-Za-z0-9_]{5,32}|-?[0-9]{1,20})$`)

// MenuButton of the main menu. Built-in actions open screens; "url" opens a link, "page"
// a text of the admin's.
type MenuButton struct {
	ID     string `json:"id" doc:"Постоянный id кнопки"`
	Action string `json:"action" enum:"sub,devices,connect,renew,support,app,url,page"`
	Label  string `json:"label"`
	On     bool   `json:"on"`
	Row    bool   `json:"row" doc:"В одном ряду с предыдущей"`
	URL    string `json:"url,omitempty" doc:"Для action=url: https:// или tg://"`
	Text   string `json:"text,omitempty" doc:"Для action=page: текст страницы"`
}

// Texts the admin writes. Variables: {name} {brand} {until} {days} {used} {left} {limit}
// {devices} {reset} {link} (the subscription link); an empty text is the built-in one.
type Texts struct {
	Welcome    string `json:"welcome" doc:"Для тех, у кого ещё нет подписки в боте"`
	Main       string `json:"main" doc:"Шапка главного меню"`
	Renew      string `json:"renew" doc:"Экран «Продлить»"`
	Expiring   string `json:"expiring" doc:"Уведомление: подписка скоро закончится"`
	Expired    string `json:"expired" doc:"Уведомление: подписка закончилась"`
	Traffic90  string `json:"traffic_90" doc:"Уведомление: израсходовано 90% трафика"`
	TrafficEnd string `json:"traffic_end" doc:"Уведомление: трафик закончился"`
	Channel    string `json:"channel" doc:"Экран обязательной подписки на канал, вместо меню; {brand}: название сервиса"`
	FreeButton string `json:"free_button" doc:"Надпись кнопки бесплатного тарифа, до 40 символов"`
}

type Notify struct {
	Expire3d   bool `json:"expire_3d"`
	Expire1d   bool `json:"expire_1d"`
	Expired    bool `json:"expired"`
	Traffic90  bool `json:"traffic_90"`
	Traffic100 bool `json:"traffic_100"`
}

// Built-in actions, each at most once in the menu.
var builtins = []string{"sub", "devices", "connect", "renew", "support", "app"}

// Default is the menu of a fresh bot in lang, "en" or else Russian.
func Default(lang string) Config {
	if lang != "en" {
		lang = "ru"
	}
	l := func(ru, en string) string {
		if lang == "en" {
			return en
		}
		return ru
	}
	return Config{
		Lang: lang,
		Buttons: []MenuButton{
			{ID: "sub", Action: "sub", Label: l("📊 Подписка", "📊 Subscription"), On: true},
			{ID: "devices", Action: "devices", Label: l("📱 Устройства", "📱 Devices"), On: true, Row: true},
			{ID: "connect", Action: "connect", Label: l("🔌 Подключить устройство", "🔌 Connect a device"), On: true},
			{ID: "renew", Action: "renew", Label: l("💳 Продлить", "💳 Renew"), On: true},
			{ID: "support", Action: "support", Label: l("💬 Поддержка", "💬 Support"), On: true, Row: true},
			{ID: "app", Action: "app", Label: l("🌐 Открыть страницу подписки", "🌐 Open the subscription page"), On: true},
		},
		Notify:      Notify{Expire3d: true, Expire1d: true, Expired: true, Traffic90: true, Traffic100: true},
		MiniApp:     true,
		CleanChat:   true,
		QuietNight:  true,
		PromoButton: true,
	}
}

// Limits keep the menu within Telegram's: 100 buttons, 64-byte callback data, 4096-char
// messages.
const (
	maxButtons = 20
	maxLabel   = 40
	maxText    = 3000
)

// Validation codes (the UI translates tg_<code>).
var (
	ErrButtons     = errors.New("tg_buttons")
	ErrButtonLabel = errors.New("tg_button_label")
	ErrButtonURL   = errors.New("tg_button_url")
	ErrButtonText  = errors.New("tg_button_text")
	ErrText        = errors.New("tg_text")
	ErrChannel     = errors.New("tg_channel")
	ErrChannelURL  = errors.New("tg_channel_url")
)

// Validate checks a config from the admin panel and fills in defaults.
func (c *Config) Validate() error {
	if c.Lang != "en" {
		c.Lang = "ru"
	}
	if len(c.Buttons) > maxButtons {
		return ErrButtons
	}
	seen := map[string]bool{}
	for i := range c.Buttons {
		b := &c.Buttons[i]
		b.Label = strings.TrimSpace(b.Label)
		if b.Label == "" || utf8.RuneCountInString(b.Label) > maxLabel {
			return ErrButtonLabel
		}
		switch b.Action {
		case "url":
			if !safeURL(b.URL) {
				return ErrButtonURL
			}
		case "page":
			if strings.TrimSpace(b.Text) == "" || utf8.RuneCountInString(b.Text) > maxText {
				return ErrButtonText
			}
		default:
			if !slices.Contains(builtins, b.Action) || seen[b.Action] {
				return ErrButtons
			}
			seen[b.Action] = true
			b.ID = b.Action
		}
		if b.Action == "url" || b.Action == "page" {
			// Custom buttons keep their id: the callback data points at it.
			if b.ID == "" || slices.Contains(builtins, b.ID) || len(b.ID) > 16 || strings.ContainsAny(b.ID, ": ") || seen["id:"+b.ID] {
				b.ID = "c" + strconv.Itoa(i+1)
			}
			seen["id:"+b.ID] = true
			if b.Action == "url" {
				b.Text = ""
			} else {
				b.URL = ""
			}
		}
	}
	for _, t := range []string{c.Texts.Welcome, c.Texts.Main, c.Texts.Renew, c.Texts.Expiring, c.Texts.Expired, c.Texts.Traffic90, c.Texts.TrafficEnd, c.Texts.Channel} {
		if utf8.RuneCountInString(t) > maxText {
			return ErrText
		}
	}
	c.Texts.FreeButton = strings.TrimSpace(c.Texts.FreeButton)
	if utf8.RuneCountInString(c.Texts.FreeButton) > maxLabel {
		return ErrButtonLabel
	}
	return c.Channel.validate()
}

// validate trims the channel and checks it can be asked about and joined: a channel by
// its numeric id has no public link, so the admin's link is required for it.
func (ch *Channel) validate() error {
	ch.Chat, ch.URL = strings.TrimSpace(ch.Chat), strings.TrimSpace(ch.URL)
	switch {
	case ch.Chat == "" && ch.Required:
		return ErrChannel
	case ch.Chat != "" && !channelChat.MatchString(ch.Chat):
		return ErrChannel
	case ch.URL != "" && !safeURL(ch.URL):
		return ErrChannelURL
	case ch.Chat != "" && ch.Link() == "":
		return ErrChannelURL
	}
	return nil
}

// safeURL: links the bot may show — web pages and Telegram links.
func safeURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" && u.Host != "" || u.Scheme == "tg" && u.Host != "") && u.User == nil
}
