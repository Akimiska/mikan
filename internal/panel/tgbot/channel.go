package tgbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"mikan/internal/panel/billing"
)

// The channel a subscriber must join before the bot shows its menu (Config.Channel), and
// the free plan, which may ask for the same channel. Callback data: gc checks the channel
// again, fp takes the free plan.

// memberTTL: how long an account seen in the channel is not asked about again. Leaving the
// channel is noticed this much later at most; a tap does not wait on Telegram meanwhile.
const memberTTL = 5 * time.Minute

// probeTTL: how often the admin's page asks Telegram whether the bot is the channel's admin.
const probeTTL = time.Minute

// maxMembers bounds the accounts kept as seen in the channel; past it the stale go.
const maxMembers = 10000

// ungated are the screens the channel never stands in front of: paying, and renewing or
// buying traffic for a subscription the account has. A payment already started is never
// blocked, and nobody is kept from paying for what they hold.
var ungated = map[string]bool{"pn": true, "py": true, "tp": true, "t": true, "r": true, "x": true, "xk": true, "xp": true}

// member says whether tgID is in chat, asking Telegram unless the account was seen there
// in the last memberTTL (fresh: ask anyway). An error when the bot cannot ask: it is off,
// it is not the channel's admin, Telegram does not answer.
func (b *Bot) member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error) {
	now := time.Now()
	b.chanMu.Lock()
	if b.chanChat != chat {
		b.chanChat, b.members, b.chanErr, b.probedAt = chat, map[int64]time.Time{}, "", time.Time{}
	}
	until, seen := b.members[tgID]
	b.chanMu.Unlock()
	if seen && !fresh && now.Before(until) {
		return true, nil
	}
	c := b.client.Load()
	if c == nil {
		return false, ErrOff
	}
	m, err := c.ChatMember(ctx, chat, tgID)
	if err != nil && unknownMember(err) {
		// An account Telegram does not know in the chat at all is not in it.
		m, err = ChatMember{Status: "left"}, nil
	}
	b.chanMu.Lock()
	defer b.chanMu.Unlock()
	if b.chanChat != chat {
		return m.In(), err // the channel changed meanwhile: nothing to keep
	}
	if err != nil {
		code := channelErrCode(err)
		if code != b.chanErr {
			b.d.Log.Warn("telegram: the channel's members cannot be checked: the gate lets everybody in", "channel", chat, "err", errText(err))
		}
		b.chanErr = code
		return false, err
	}
	b.chanErr = ""
	if !m.In() {
		delete(b.members, tgID)
		return false, nil
	}
	if len(b.members) >= maxMembers {
		for id, t := range b.members {
			if now.After(t) {
				delete(b.members, id)
			}
		}
	}
	b.members[tgID] = now.Add(memberTTL)
	return true, nil
}

// unknownMember: Telegram has no such account in the chat, which is no fault of the bot.
func unknownMember(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 400 {
		return false
	}
	d := strings.ToLower(ae.Description)
	return strings.Contains(d, "user not found") || strings.Contains(d, "participant_id_invalid")
}

// channelErrCode names why the channel could not be asked, for the admin's page:
// channel_not_admin, channel_not_found, unreachable or Telegram's own words.
func channelErrCode(err error) string {
	var ae *APIError
	if !errors.As(err, &ae) {
		if errors.Is(err, ErrOff) {
			return ""
		}
		return "unreachable"
	}
	d := strings.ToLower(ae.Description)
	switch {
	case strings.Contains(d, "chat not found"):
		return "channel_not_found"
	case ae.Code == 403, strings.Contains(d, "inaccessible"), strings.Contains(d, "not enough rights"),
		strings.Contains(d, "administrator"), strings.Contains(d, "not a member"):
		return "channel_not_admin"
	}
	return ae.Description
}

// passes: chat may see the menu. The gate is off, the account is in the channel, or the
// channel cannot be asked: then nobody is locked out for the panel's trouble, the log
// and the admin's page say what is wrong.
func (b *Bot) passes(ctx context.Context, cfg Config, chat int64, fresh bool) bool {
	if !cfg.Channel.gate() {
		return true
	}
	in, err := b.member(ctx, cfg.Channel.Chat, chat, fresh)
	return in || err != nil
}

// gateScreen stands in front of the menu: the admin's text, a button to the channel and
// one that checks again.
func (b *Bot) gateScreen(ctx context.Context, cfg Config, w *words, notice string) (string, *Keyboard) {
	text := render(pick(cfg.Texts.Channel, w.channel), map[string]string{"brand": b.brand(ctx)})
	if notice != "" {
		text = html.EscapeString(notice) + "\n\n" + text
	}
	return text, &Keyboard{channelButtons(cfg, w, "gc")}
}

// channelButtons: the link to join the channel and a check that goes to data.
func channelButtons(cfg Config, w *words, data string) [][]Button {
	var rows [][]Button
	if link := cfg.Channel.Link(); link != "" {
		rows = append(rows, []Button{{Text: w.channelJoin, URL: link}})
	}
	return append(rows, []Button{{Text: w.channelCheck, CallbackData: data}})
}

// Channel implements billing.Telegram: the link to join the bot's channel.
func (b *Bot) Channel(ctx context.Context) (string, bool) {
	link := b.Config(ctx).Channel.Link()
	return link, link != ""
}

// Subscribed implements billing.Telegram: tgID is in the bot's channel, asked as the gate
// asks (an account seen there lately is not asked about again).
func (b *Bot) Subscribed(ctx context.Context, tgID int64) (bool, error) {
	chat := b.Config(ctx).Channel.Chat
	if chat == "" {
		return false, ErrOff
	}
	return b.member(ctx, chat, tgID, false)
}

// ChannelError is why the bot cannot check its channel's members, for the admin's page:
// channel_not_admin, channel_not_found, unreachable or Telegram's words; "" when it can,
// no channel is set up, or the bot is off. The bot asks about itself there at most once
// a probeTTL (and again after a save), and a failed check of a subscriber counts too.
func (b *Bot) ChannelError(ctx context.Context) string {
	chat := b.Config(ctx).Channel.Chat
	c, st := b.client.Load(), b.Status()
	if chat == "" || c == nil || st.Bot.ID == 0 {
		return ""
	}
	b.chanMu.Lock()
	if b.chanChat == chat && time.Since(b.probedAt) < probeTTL {
		code := b.chanErr
		b.chanMu.Unlock()
		return code
	}
	b.chanMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m, err := c.ChatMember(ctx, chat, st.Bot.ID)
	code := ""
	switch {
	case err != nil:
		code = channelErrCode(err)
	case m.Status != "administrator" && m.Status != "creator":
		code = "channel_not_admin"
	}
	b.chanMu.Lock()
	defer b.chanMu.Unlock()
	if b.chanChat != chat {
		b.chanChat, b.members = chat, map[int64]time.Time{}
	}
	b.chanErr, b.probedAt = code, time.Now()
	return code
}

// freeButton is the free plan's button, while the account may take it.
func (b *Bot) freeButton(ctx context.Context, cfg Config, w *words, chat int64) (Button, bool) {
	if b.d.Billing == nil {
		return Button{}, false
	}
	if _, ok := b.d.Billing.FreeOffer(ctx, chat); !ok {
		return Button{}, false
	}
	return Button{Text: pick(cfg.Texts.FreeButton, w.freeButton), CallbackData: "fp"}, true
}

// takeFree gives the chat the free plan and says what it got. An account the plan's
// channel does not see yet gets the channel's buttons, the check taking the plan again.
func (b *Bot) takeFree(ctx context.Context, cfg Config, w *words, chat int64) (string, *Keyboard) {
	home := &Keyboard{[][]Button{{{Text: w.back, CallbackData: "m"}}}}
	if b.d.Billing == nil {
		return html.EscapeString(w.freeOff), home
	}
	u, err := b.d.Billing.FreePlan(ctx, chat)
	switch {
	case errors.Is(err, billing.ErrFreeChannel):
		return html.EscapeString(w.freeChannel), &Keyboard{append(channelButtons(cfg, w, "fp"), home.InlineKeyboard...)}
	case errors.Is(err, billing.ErrFreeActive):
		return html.EscapeString(w.freeActive), home
	case errors.Is(err, billing.ErrFreeOff):
		return html.EscapeString(w.freeOff), home
	case errors.Is(err, billing.ErrTooManySubs):
		return html.EscapeString(fmt.Sprintf(w.linkLimit, MaxLinks)), home
	case err != nil:
		b.d.Log.Warn("telegram: free plan", "tg", chat, "err", err)
		return html.EscapeString(w.freeFail), home
	}
	what := u.Name
	if t, err := b.d.Store.Q.GetTariff(ctx, u.TariffID.Int64); err == nil {
		what = billing.Describe(t, t.DurationDays, cfg.Lang)
	}
	return fmt.Sprintf(html.EscapeString(w.freeDone), html.EscapeString(what)), &Keyboard{[][]Button{{{Text: w.trialOpen, CallbackData: "m"}}}}
}
