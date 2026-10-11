package tgbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/panelimport"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
)

// KeyConfig is the admin's setup of the bot (Config), saved where the gateway reads its
// language and channel.
const KeyConfig = tgate.KeyConfig

// MaxLinks: subscriptions one Telegram account may hold.
const MaxLinks = tgate.MaxLinks

// Deps is what the bot needs from the panel.
type Deps struct {
	Store    *store.Store
	Settings *settings.Settings
	Devices  *domain.Devices
	// Users reissues a subscription's link at its subscriber's request.
	Users *domain.Users
	// SubBase is https://host:port/<sub path>, "" while the panel has no public address.
	SubBase func(ctx context.Context) string
	// MiniApp: the panel's certificate is one Telegram apps accept (not self-signed).
	MiniApp func() bool
	Log     *slog.Logger
	Now     func() time.Time
	// Billing sells tariffs in the menu; nil: no shop.
	Billing *billing.Service
	// Gateway gets the updates to the bot and sends what it says.
	Gateway *tgate.Gateway
}

// Bot is the bot's screens: the menu, the shop, the notices, the broadcasts. It gets its
// updates from the gateway (Update) and sends through the gateway's outbox.
type Bot struct {
	d Deps

	mu    sync.Mutex
	bcast BroadcastProgress
	// Subscriptions somebody asked to take over: by subscription, and who was refused.
	transfers map[int64]transfer
	refused   map[string]time.Time

	noticeMu sync.Mutex
	notices  map[string]*noticeState

	// The rounds of notices the bot started and has not ended: Run returns when they have,
	// so that the database is not closed under them.
	running   sync.WaitGroup
	notifying atomic.Bool // a round of notices is being made
}

// BroadcastProgress is the last broadcast as the admin panel shows it.
type BroadcastProgress struct {
	Total, Sent, Failed int
	Started             time.Time
}

// Active: some of it is still being sent.
func (p BroadcastProgress) Active() bool { return p.Sent+p.Failed < p.Total }

func New(d Deps) *Bot {
	return &Bot{d: d, transfers: map[int64]transfer{}, refused: map[string]time.Time{}, notices: map[string]*noticeState{}}
}

// Run sends the notifications while the bot is on; the gateway runs the bot itself.
func (b *Bot) Run(ctx context.Context) {
	notify := time.NewTicker(10 * time.Minute)
	defer notify.Stop()
	for {
		select {
		case <-ctx.Done():
			b.running.Wait()
			return
		case <-notify.C:
			// Apart from this loop; one round at a time.
			if b.notifying.CompareAndSwap(false, true) {
				b.running.Go(func() {
					defer b.notifying.Store(false)
					b.Notify(ctx)
				})
			}
		}
	}
}

// Config is the admin's setup, or the default one in the panel's default language.
func (b *Bot) Config(ctx context.Context) Config { return b.config(ctx, b.d.Settings) }

func (b *Bot) config(ctx context.Context, set *settings.Settings) Config {
	lang, _ := set.Lang(ctx)
	// A setup saved before an option existed gets that option's default. A saved menu
	// replaces the default one whole.
	def := Default(lang)
	def.Buttons = nil
	cfg, ok, err := settings.GetOver(ctx, set, KeyConfig, def)
	if err != nil || !ok {
		return Default(lang)
	}
	_ = cfg.Validate()
	return cfg
}

// Started implements tgate.Handler: the command menu and the Mini App's button beside the
// input field, in the bot's language.
func (b *Bot) Started(ctx context.Context, c *tgate.Client) {
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	_ = c.SetCommands(ctx, w.commands)
	if url := b.miniAppURL(ctx, cfg); url != "" {
		_ = c.SetMenuButton(ctx, w.menuApp, url)
	} else {
		_ = c.SetMenuButton(ctx, "", "")
	}
}

// Update implements tgate.Handler: a message or a press in a private chat, the flood guard
// passed. It is answered, or dropped, as it comes.
func (b *Bot) Update(ctx context.Context, c *tgate.Client, out *tgate.Outbox, up tgate.Update) {
	switch {
	case up.CallbackQuery != nil && up.CallbackQuery.Message != nil:
		if cmd, _, _ := strings.Cut(up.CallbackQuery.Data, ":"); cmd == "ta" || cmd == "tx" {
			b.onTransfer(ctx, c, out, up.CallbackQuery)
		} else {
			b.onPress(ctx, c, out, up.CallbackQuery)
		}
	case up.Message != nil && up.Message.From != nil:
		b.onMessage(ctx, out, up.Message)
	}
}

var (
	linkInText  = regexp.MustCompile(`https?://\S+`)
	nativeToken = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)
)

// maxLinksRead: how many addresses of one message are looked at, so that a long list is
// not a hundred database lookups.
const maxLinksRead = 3

// onMessage: /start (with a code from a subscription page), a subscription link, or
// anything else — every message brings the main menu back to the bottom of the chat.
func (b *Bot) onMessage(ctx context.Context, out *tgate.Outbox, m *tgate.Message) {
	chat := m.Chat.ID
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	if cfg.CleanChat {
		id := m.MessageID
		out.Reply(chat, "", 1, func(ctx context.Context, c *tgate.Client) error { return c.Delete(ctx, chat, id) })
	}
	var notice string
	text := strings.TrimSpace(m.Text)
	switch {
	case strings.HasPrefix(text, "/start "):
		notice = b.linkByCode(ctx, out, w, chat, who(m.From), strings.TrimSpace(strings.TrimPrefix(text, "/start ")))
	default:
		notice = b.linkByAddress(ctx, out, w, chat, who(m.From), text)
	}
	b.freshMenu(out, chat, notice)
}

// freshMenu sends the main menu as a new message and removes the previous one, so the
// chat keeps a single menu at the bottom. Several messages in a row get one new menu.
func (b *Bot) freshMenu(out *tgate.Outbox, chat int64, notice string) {
	out.Reply(chat, "menu:"+strconv.FormatInt(chat, 10), 2, func(ctx context.Context, c *tgate.Client) error {
		return b.sendMenu(ctx, c, chat, notice)
	})
}

func (b *Bot) sendMenu(ctx context.Context, c *tgate.Client, chat int64, notice string) error {
	text, kb := b.screen(ctx, b.Config(ctx), chat, "m", notice)
	sent, err := c.Send(ctx, chat, text, kb, false)
	if err != nil {
		return err
	}
	if old, err := b.d.Store.Q.GetTgChat(ctx, chat); err == nil && old.MenuMsgID != 0 && old.MenuMsgID != sent.MessageID {
		_ = c.Delete(ctx, chat, old.MenuMsgID)
	}
	_ = b.d.Store.Q.SetTgMenu(ctx, db.SetTgMenuParams{MenuMsgID: sent.MessageID, TgID: chat})
	return nil
}

// answer stops the spinner on a pressed button. A call to Telegram that hangs must not
// hold up the update loop, so it goes on its own.
func (b *Bot) answer(ctx context.Context, c *tgate.Client, id string) {
	b.d.Gateway.Go(func() { _ = c.Answer(ctx, id, "") })
}

// onPress edits the menu message in place: the chat never fills up with menus. The
// spinner on the button stops at once; the screen is drawn when the chat's turn comes,
// and a newer tap on the same menu replaces a screen not yet drawn.
func (b *Bot) onPress(ctx context.Context, c *tgate.Client, out *tgate.Outbox, q *tgate.CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	b.answer(ctx, c, q.ID)
	// What a tap changes happens now, only its screen may wait.
	data, notice := b.act(ctx, chat, q.Data)
	out.Reply(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), 1, func(ctx context.Context, c *tgate.Client) error {
		text, kb := b.screen(ctx, b.Config(ctx), chat, data, notice)
		if err := c.Edit(ctx, chat, msg, text, kb); err != nil {
			var ae *tgate.APIError
			if errors.As(err, &ae) && ae.Code == 429 {
				return err
			}
			// Too old to edit, or gone: a new menu instead.
			return b.sendMenu(ctx, c, chat, notice)
		}
		_ = b.d.Store.Q.SetTgMenu(ctx, db.SetTgMenuParams{MenuMsgID: msg, TgID: chat})
		return nil
	})
}

func (b *Bot) linkByCode(ctx context.Context, out *tgate.Outbox, w *words, chat int64, name, code string) string {
	id, err := b.d.Gateway.UserOfCode(ctx, code)
	switch {
	case errors.Is(err, tgate.ErrLinkExpired):
		return w.linkExpired
	case err != nil:
		return w.linkInvalid
	}
	return b.link(ctx, out, w, chat, name, id)
}

// linkByAddress takes the subscription address in a message: the panel's own, or one of
// the panel the users came from. "" when the message has no address with a path at all;
// an address that leads to no subscription is told so.
func (b *Bot) linkByAddress(ctx context.Context, out *tgate.Outbox, w *words, chat int64, name, text string) string {
	var seen bool
	for _, raw := range linkInText.FindAllString(text, maxLinksRead) {
		u, err := url.Parse(strings.TrimRight(raw, ".,;:!?)]}>\"'»"))
		if err != nil || u.Host == "" {
			continue
		}
		if u.Path != "" && u.Path != "/" {
			seen = true
		}
		if id, ok := b.userOfAddress(ctx, u.Path); ok {
			return b.link(ctx, out, w, chat, name, id)
		}
	}
	if seen {
		return w.linkInvalid
	}
	return ""
}

// userOfAddress finds whose subscription an address path leads to. The own token is a
// path segment of 24 letters and digits. An old panel's address is taken as the panel
// serves it (panelimport.LinkToken), and only while old links are set up; the signature
// of a token Marzban or PasarGuard signed is checked as there (Verifier.User).
func (b *Bot) userOfAddress(ctx context.Context, urlPath string) (int64, bool) {
	for seg := range strings.SplitSeq(urlPath, "/") {
		if !nativeToken.MatchString(seg) {
			continue
		}
		if u, err := b.d.Store.Q.GetUserBySubToken(ctx, seg); err == nil {
			return u.ID, true
		}
	}
	if b.d.Settings == nil {
		return 0, false
	}
	legacyPath, verifier, err := panelimport.LoadLegacy(ctx, b.d.Settings)
	if err != nil {
		return 0, false
	}
	token, _, ok := panelimport.LinkToken(urlPath, legacyPath)
	if !ok {
		return 0, false
	}
	if u, ok := verifier.User(ctx, b.d.Store.Q, token); ok {
		return u.ID, true
	}
	return 0, false
}

// link ties a subscription to the chat's account. A subscription has one owner. The link
// to it is no proof of being the owner (it gets forwarded, shown on screenshots), so one
// that is already linked is moved only with its owner's yes, asked in the owner's own chat;
// an owner who cannot be asked, as they blocked the bot, is not in the way.
func (b *Bot) link(ctx context.Context, out *tgate.Outbox, w *words, chat int64, name string, userID int64) string {
	u, err := b.d.Store.Q.GetUser(ctx, userID)
	if err != nil {
		return w.linkInvalid
	}
	prev, err := b.d.Store.Q.GetTgLink(ctx, userID)
	switch {
	case err == nil && prev.TgID == chat:
		_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: chat})
		return fmt.Sprintf(w.alreadyLinked, u.Name)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return w.linkInvalid
	}
	if n, err := b.d.Store.Q.CountTgLinksOf(ctx, chat); err != nil || n >= MaxLinks {
		return fmt.Sprintf(w.linkLimit, MaxLinks)
	}
	if prev.TgID != 0 && b.reachable(ctx, prev.TgID) {
		return b.askOwner(ctx, out, w, chat, name, u, prev.TgID)
	}
	if err := b.d.Store.Q.LinkTg(ctx, db.LinkTgParams{UserID: userID, TgID: chat, CreatedAt: b.d.Now().Unix()}); err != nil {
		return w.linkInvalid
	}
	_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: chat})
	return fmt.Sprintf(w.linked, u.Name)
}

// reachable: the account has a chat with the bot and has not blocked it, so it can be asked.
func (b *Bot) reachable(ctx context.Context, tgID int64) bool {
	ch, err := b.d.Store.Q.GetTgChat(ctx, tgID)
	return err == nil && ch.Blocked == 0
}

// who is how a Telegram account is named to the owner of a subscription it asks for.
func who(u *tgate.User) string {
	switch {
	case u == nil:
		return "?"
	case u.Username != "":
		return "@" + u.Username
	case u.FirstName != "":
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

// transfer is another account's request for a subscription, open until the owner answers
// or the time is up.
type transfer struct {
	to    int64
	until time.Time
}

const (
	transferTTL    = time.Hour
	transferRefuse = 24 * time.Hour // after a refusal, the same account does not ask again for this long
)

func refusedKey(userID, chat int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(chat, 10)
}

// askOwner sends the subscription's owner the question and tells the one who asked to wait.
func (b *Bot) askOwner(ctx context.Context, out *tgate.Outbox, w *words, chat int64, name string, u db.User, owner int64) string {
	now := b.d.Now()
	b.mu.Lock()
	t, open := b.transfers[u.ID]
	if open && now.After(t.until) {
		delete(b.transfers, u.ID)
		open = false
	}
	switch {
	case open && t.to == chat:
		b.mu.Unlock()
		return w.transferAsked
	case open:
		b.mu.Unlock()
		return w.transferBusy
	case now.Before(b.refused[refusedKey(u.ID, chat)]):
		b.mu.Unlock()
		return w.transferDeniedNew
	}
	b.transfers[u.ID] = transfer{to: chat, until: now.Add(transferTTL)}
	b.mu.Unlock()
	text := html.EscapeString(fmt.Sprintf(w.transferAsk, u.Name, name))
	data := strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(chat, 10)
	kb := &tgate.Keyboard{InlineKeyboard: [][]tgate.Button{{{Text: w.transferAllow, CallbackData: "ta:" + data}, {Text: w.transferDeny, CallbackData: "tx:" + data}}}}
	out.Notice(owner, func(ctx context.Context, c *tgate.Client) error {
		_, err := c.Send(ctx, owner, text, kb, false)
		return err
	}, func(err error) {
		if err != nil { // the owner was not reached: the request lapses, and can be made again
			b.mu.Lock()
			delete(b.transfers, u.ID)
			b.mu.Unlock()
		}
	})
	return w.transferAsked
}

// onTransfer is the owner's answer to askOwner. It counts only from the owner's own chat,
// for the very request that is open, and while it is.
func (b *Bot) onTransfer(ctx context.Context, c *tgate.Client, out *tgate.Outbox, q *tgate.CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	b.answer(ctx, c, q.ID)
	cmd, arg, _ := strings.Cut(q.Data, ":")
	uidStr, toStr, _ := strings.Cut(arg, ":")
	userID, _ := strconv.ParseInt(uidStr, 10, 64)
	to, _ := strconv.ParseInt(toStr, 10, 64)
	w := wordsFor(b.lang(ctx))
	say := func(text string) { // the question's message becomes the answer, without buttons
		out.Reply(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), 1, func(ctx context.Context, c *tgate.Client) error {
			return c.Edit(ctx, chat, msg, html.EscapeString(text), nil)
		})
	}
	tell := func(to int64, text string) {
		out.Notice(to, func(ctx context.Context, c *tgate.Client) error {
			_, err := c.Send(ctx, to, html.EscapeString(text), nil, false)
			return err
		}, nil)
	}
	now := b.d.Now()
	b.mu.Lock()
	t, open := b.transfers[userID]
	link, err := b.d.Store.Q.GetTgLink(ctx, userID)
	if !open || t.to != to || now.After(t.until) || err != nil || link.TgID != chat {
		b.mu.Unlock()
		say(w.transferStale)
		return
	}
	delete(b.transfers, userID)
	if cmd == "tx" {
		b.refused[refusedKey(userID, to)] = now.Add(transferRefuse)
		if len(b.refused) > 1000 {
			for k, until := range b.refused {
				if now.After(until) {
					delete(b.refused, k)
				}
			}
		}
	}
	b.mu.Unlock()
	u, err := b.d.Store.Q.GetUser(ctx, userID)
	if err != nil {
		say(w.transferStale)
		return
	}
	if cmd == "tx" {
		say(fmt.Sprintf(w.transferDenied, u.Name))
		tell(to, w.transferDeniedNew)
		return
	}
	if n, err := b.d.Store.Q.CountTgLinksOf(ctx, to); err != nil || n >= MaxLinks {
		say(w.transferStale)
		tell(to, fmt.Sprintf(w.linkLimit, MaxLinks))
		return
	}
	if err := b.d.Store.Q.LinkTg(ctx, db.LinkTgParams{UserID: userID, TgID: to, CreatedAt: now.Unix()}); err != nil {
		say(w.transferStale)
		return
	}
	_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: to})
	say(fmt.Sprintf(w.transferDone, u.Name))
	b.freshMenu(out, to, fmt.Sprintf(w.transferNotice, u.Name))
}

// subs are the chat's subscriptions and the one it shows.
func (b *Bot) subs(ctx context.Context, chat int64) ([]db.User, db.User, bool) {
	list, err := b.d.Store.Q.ListTgLinksOf(ctx, chat)
	if err != nil || len(list) == 0 {
		return nil, db.User{}, false
	}
	cur := list[0]
	if ch, err := b.d.Store.Q.GetTgChat(ctx, chat); err == nil {
		for _, u := range list {
			if u.ID == ch.Current {
				cur = u
			}
		}
	}
	return list, cur, true
}

func (b *Bot) miniAppURL(ctx context.Context, cfg Config) string {
	base := b.d.SubBase(ctx)
	if !cfg.MiniApp || base == "" || b.d.MiniApp == nil || !b.d.MiniApp() {
		return ""
	}
	return base + "/tg"
}

func labelOf(cfg Config, action, def string) string {
	for _, btn := range cfg.Buttons {
		if btn.Action == action {
			return btn.Label
		}
	}
	return def
}

// MiniAppURL is where the bot's Mini App opens; "" when Telegram could not load it.
func (b *Bot) MiniAppURL(ctx context.Context) string { return b.miniAppURL(ctx, b.Config(ctx)) }
