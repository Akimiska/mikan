package tgbot

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// withChannel saves the default bot with ch as its channel.
func (e *env) withChannel(ch Channel, texts ...func(*Texts)) {
	e.t.Helper()
	cfg := Default("ru")
	cfg.Channel = ch
	for _, f := range texts {
		f(&cfg.Texts)
	}
	if err := cfg.Validate(); err != nil {
		e.t.Fatal(err)
	}
	if err := settings.Set(e.ctx, e.set, KeyConfig, cfg); err != nil {
		e.t.Fatal(err)
	}
}

// start sends /start from an account and returns the menu it gets.
func (e *env) start(from int64) call {
	e.t.Helper()
	e.later()
	n := e.tg.count()
	e.say(from, "/start")
	send, _ := find(e.tg.wait(e.t, n, "sendMessage"), "sendMessage")
	return send
}

// tap presses a button of the account's menu and returns the screen it gets.
func (e *env) tap(from int64, data string) call {
	e.t.Helper()
	e.later()
	n := e.tg.count()
	e.press(from, 1000, data)
	edit, _ := find(e.tg.wait(e.t, n, "editMessageText"), "editMessageText")
	return edit
}

// The mandatory channel: a stranger gets its screen instead of the welcome or the menu, the
// check lets a member in, and paying for what an account holds is never stood in front of.
func TestBotChannelGate(t *testing.T) {
	e := setup(t)
	const visitor, anna = 790, 555
	e.withChannel(Channel{Required: true, Chat: "@mikan_news"})
	gate := e.start(visitor)
	if !strings.Contains(text(gate), "подпишитесь на наш канал") || buttons(gate)["📢 Подписаться на канал"] != "https://t.me/mikan_news" || buttons(gate)["✅ Проверить"] != "gc" {
		t.Fatalf("the channel's screen: %q %v", text(gate), buttons(gate))
	}
	if again := e.tap(visitor, "gc"); !strings.Contains(text(again), "Пока не видим вас") {
		t.Fatalf("a check before joining: %q", text(again))
	}
	e.tg.join(visitor, "member")
	if in := e.tap(visitor, "gc"); strings.Contains(text(in), "подпишитесь") || !strings.Contains(text(in), "Mikan VPN") {
		t.Fatalf("a check after joining: %q", text(in))
	}
	// A customer who is not in the channel: no menu, but renewing is not stood in front of.
	if err := e.st.Q.LinkTg(e.ctx, db.LinkTgParams{UserID: e.user.ID, TgID: anna, CreatedAt: e.now.Unix()}); err != nil {
		t.Fatal(err)
	}
	if menu := e.start(anna); !strings.Contains(text(menu), "подпишитесь на наш канал") {
		t.Fatalf("a customer outside the channel got the menu: %q", text(menu))
	}
	if renew := e.tap(anna, "r"); strings.Contains(text(renew), "подпишитесь") {
		t.Fatalf("renewing is stood in front of: %q", text(renew))
	}
	// A restricted account that is still in the channel is in it.
	e.tg.join(791, "restricted")
	if menu := e.start(791); strings.Contains(text(menu), "подпишитесь") {
		t.Fatalf("a restricted member: %q", text(menu))
	}
	// One seen in the channel is not asked about again for a while; the check asks anyway.
	e.tg.join(visitor, "left")
	if menu := e.start(visitor); strings.Contains(text(menu), "подпишитесь") {
		t.Fatalf("a member seen a moment ago is asked again: %q", text(menu))
	}
	if gone := e.tap(visitor, "gc"); !strings.Contains(text(gone), "Пока не видим вас") {
		t.Fatalf("the check trusted the cache: %q", text(gone))
	}
	if code := e.bot.ChannelError(e.ctx); code != "" {
		t.Fatalf("a channel the bot runs: %q", code)
	}
}

// A channel the bot is not an admin of locks nobody out, and the admin's page says why.
func TestBotChannelNotAdmin(t *testing.T) {
	e := setup(t)
	e.withChannel(Channel{Required: true, Chat: "-1001234567890", URL: "https://t.me/+invite"})
	e.tg.mu.Lock()
	e.tg.notAdmin = true
	e.tg.mu.Unlock()
	if menu := e.start(792); strings.Contains(text(menu), "подпишитесь") || !strings.Contains(text(menu), "Mikan VPN") {
		t.Fatalf("a channel the bot cannot ask about locked a visitor out: %q", text(menu))
	}
	if code := e.bot.ChannelError(e.ctx); code != "channel_not_admin" {
		t.Fatalf("the admin is not told: %q", code)
	}
	// The switch off: nothing stands in front of the menu, whatever the channel.
	e.withChannel(Channel{Chat: "@mikan_news"})
	e.tg.mu.Lock()
	e.tg.notAdmin = false
	e.tg.mu.Unlock()
	if menu := e.start(793); strings.Contains(text(menu), "подпишитесь") {
		t.Fatalf("a channel that is not required: %q", text(menu))
	}
}

// The free plan in the bot: a button with the admin's words, the channel asked for when
// the plan needs it, a subscription from the free plan, and no second one while it runs.
func TestBotFreePlan(t *testing.T) {
	var svc *billing.Service
	e := setup(t, func(e *env, d *Deps) {
		svc = billing.New(billing.Deps{Store: e.st, Settings: e.set, Users: domain.NewUsers(e.st, domain.NewPool(e.st, e.clock), noChanges{}, e.clock),
			Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock, MaxLinks: MaxLinks})
		d.Billing = svc
	})
	svc.SetTelegram(e.bot)
	const visitor = 795
	if _, ok := buttons(e.start(visitor))["🎁 Бесплатный тариф"]; ok {
		t.Fatal("a free plan nobody offers")
	}
	ts, _ := e.st.Q.ListTariffs(e.ctx)
	if err := settings.Set(e.ctx, e.set, billing.KeyConfig, billing.Config{FreeTariffID: ts[0].ID, FreeCondition: billing.FreeChannel}); err != nil {
		t.Fatal(err)
	}
	// The plan asks for a channel the bot does not have: it is off.
	if _, ok := buttons(e.start(visitor))["🎁 Бесплатный тариф"]; ok {
		t.Fatal("a free plan for a channel nobody set up")
	}
	e.withChannel(Channel{Chat: "@mikan_news"}, func(t *Texts) { t.FreeButton = "🎁 Даром" })
	if buttons(e.start(visitor))["🎁 Даром"] != "fp" {
		t.Fatal("the welcome does not offer the free plan")
	}
	ask := e.tap(visitor, "fp")
	if !strings.Contains(text(ask), "для подписчиков нашего канала") || buttons(ask)["📢 Подписаться на канал"] != "https://t.me/mikan_news" || buttons(ask)["✅ Проверить"] != "fp" {
		t.Fatalf("the free plan outside the channel: %q %v", text(ask), buttons(ask))
	}
	if links, _ := e.st.Q.ListTgLinksOf(e.ctx, visitor); len(links) != 0 {
		t.Fatalf("a stranger got the free plan: %+v", links)
	}
	e.tg.join(visitor, "member")
	got := e.tap(visitor, "fp")
	if !strings.Contains(text(got), "Бесплатная подписка готова") || buttons(got)["📱 Открыть подписку"] != "m" {
		t.Fatalf("the free plan: %q %v", text(got), buttons(got))
	}
	links, _ := e.st.Q.ListTgLinksOf(e.ctx, visitor)
	if len(links) != 1 || links[0].Source != domain.UserFromFree {
		t.Fatalf("links after the free plan: %+v", links)
	}
	if _, ok := buttons(e.start(visitor))["🎁 Даром"]; ok {
		t.Fatal("the free plan is offered while the first runs")
	}
	if again := e.tap(visitor, "fp"); !strings.Contains(text(again), "уже есть") {
		t.Fatalf("a second tap: %q", text(again))
	}
	// The plan off: the button goes, the subscription stays.
	if err := settings.Set(e.ctx, e.set, billing.KeyConfig, billing.Config{}); err != nil {
		t.Fatal(err)
	}
	if off := e.tap(visitor, "fp"); !strings.Contains(text(off), "недоступен") {
		t.Fatalf("a free plan turned off: %q", text(off))
	}
	if links, _ := e.st.Q.ListTgLinksOf(e.ctx, visitor); len(links) != 1 {
		t.Fatalf("the free subscription went with the plan: %+v", links)
	}
}

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
		ch   Channel
		want error
	}{
		{Channel{}, nil},
		{Channel{Required: true}, ErrChannel},
		{Channel{Chat: "@abc"}, ErrChannel},
		{Channel{Chat: "mikan_news"}, ErrChannel},
		{Channel{Required: true, Chat: " @mikan_news "}, nil},
		{Channel{Chat: "-1001234567890"}, ErrChannelURL},
		{Channel{Chat: "-1001234567890", URL: "https://t.me/+abcdef"}, nil},
		{Channel{Chat: "@mikan_news", URL: "http://t.me/mikan_news"}, ErrChannelURL},
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
