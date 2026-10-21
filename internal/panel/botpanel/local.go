// Package botpanel is the panel's side of the Telegram bot: Local answers the bot addon's calls
// from the panel's store, settings and billing in the bot's model (served over the Panel API by
// RPCHandler), and Panel tells the addon of the notices that are due and sends the admin's
// broadcasts. The bot's screens are the addon's (tgbot).
package botpanel

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"mikan/internal/panel/aoiref"
	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/panelimport"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
	"mikan/internal/panel/tgbot"
)

// Backend is tgbot.Backend and what only the panel's side of the bot reads: the notices about
// the term and the traffic (which were told, tg_notices) and the broadcasts' targets. The
// addon never asks for these, so tgbot.Remote has none of them and the Panel API opens none:
// a list of every subscriber's account is not the addon's to read.
type Backend interface {
	tgbot.Backend
	// Notices are the subscriptions that may get a notice now.
	Notices(ctx context.Context, now time.Time) ([]db.ListNoticeSubscriptionsRow, error)
	NoticeSent(ctx context.Context, userID int64, kind string, period int64) (bool, error)
	NoticeDelivered(ctx context.Context, userID int64, kind string, period int64, at time.Time) error
	PruneNotices(ctx context.Context, before time.Time) error
	// BroadcastTargets are the accounts a broadcast goes to.
	BroadcastTargets(ctx context.Context) ([]int64, error)
}

// Local is the bot's Backend inside the panel's process: its store, settings, users, devices
// and billing. It is the only part of the bot that touches them; the bot addon reaches it over
// the Panel API (RPCHandler), and the panel's side of the bot (Panel) calls it directly.
type Local struct {
	Store    *store.Store
	Settings *settings.Settings
	// Binder unbinds devices at their subscriber's request.
	Binder *domain.Devices
	// Users reissues a subscription's link at its subscriber's request.
	Users *domain.Users
	// Billing sells tariffs in the menu.
	Billing *billing.Service
	// Gateway reads the link codes it signed and asks who is in the channel.
	Gateway *tgate.Gateway
	// SubBase is https://host:port/<sub path>, "" while the panel has no public address.
	SubBase func(ctx context.Context) string
	// MiniApp: the panel's certificate is one Telegram apps accept (not self-signed).
	MiniApp func() bool
	Now     func() time.Time
	// Referrals is the referral programme (AoiVPN fork); nil: none.
	Referrals *aoiref.Service
}

func (l *Local) Referral(ctx context.Context, tgID int64) (aoiref.Stats, error) {
	if l.Referrals == nil {
		return aoiref.Stats{}, errNoReferrals
	}
	return l.Referrals.Stats(ctx, tgID)
}

func (l *Local) Refer(ctx context.Context, tgID int64, code string) (bool, error) {
	if l.Referrals == nil {
		return false, errNoReferrals
	}
	return l.Referrals.Refer(ctx, tgID, code)
}

var errNoReferrals = errors.New("referrals_off")

var _ Backend = (*Local)(nil)

// nativeToken is the panel's own subscription token: 24 letters and digits.
var nativeToken = regexp.MustCompile(`^[A-Za-z0-9]{24}$`)

func (l *Local) TagCampaign(ctx context.Context, tgID int64, code string) (bool, error) {
	return domain.NewCampaigns(l.Store, l.Now).Tag(ctx, tgID, code)
}

func (l *Local) Config(ctx context.Context) tgbot.Config {
	// A setup saved before an option existed gets that option's default. A saved menu
	// replaces the default one whole.
	cfg, lang, saved := tgate.ReadSetup(ctx, l.Settings, func(lang string) tgbot.Config {
		def := tgbot.Default(lang)
		def.Buttons = nil
		return def
	})
	if !saved {
		return tgbot.Default(lang)
	}
	_ = cfg.Validate()
	return cfg
}

// Member is the channel check, for the bot's own channel only: the screens cannot probe
// other chats or reset what the gateway knows about members.
func (l *Local) Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error) {
	if ch := l.Gateway.ChannelConfig(ctx).Chat; ch == "" || chat != ch {
		return false, model.ErrNotChannel
	}
	return l.Gateway.Member(ctx, chat, tgID, fresh)
}

func (l *Local) Brand(ctx context.Context) string {
	s, _ := l.Settings.String(ctx, settings.KeyBrand)
	return s
}

func (l *Local) SupportURL(ctx context.Context) string {
	s, _ := l.Settings.String(ctx, settings.KeySupportURL)
	return s
}

func (l *Local) SubURL(ctx context.Context, u model.User) string {
	if base := l.SubBase(ctx); base != "" {
		return base + "/" + u.SubToken
	}
	return ""
}

func (l *Local) MiniAppURL(ctx context.Context) string {
	base := l.SubBase(ctx)
	if base == "" || l.MiniApp == nil || !l.MiniApp() {
		return ""
	}
	return base + "/tg"
}

func (l *Local) Context(ctx context.Context, tgID int64) (tgbot.Account, error) {
	list, err := l.Store.Q.ListTgLinksOf(ctx, tgID)
	if err != nil {
		return tgbot.Account{}, err
	}
	acc := tgbot.Account{Links: model.Each(list, func(u db.User) model.User { return model.User(u) })}
	if ch, err := l.Store.Q.GetTgChat(ctx, tgID); err == nil {
		acc.Chat, acc.HasChat = model.TgChat(ch), true
	}
	return acc, nil
}

func (l *Local) Chat(ctx context.Context, tgID int64) (model.TgChat, error) {
	ch, err := l.Store.Q.GetTgChat(ctx, tgID)
	return model.TgChat(ch), err
}

func (l *Local) SetMenu(ctx context.Context, tgID, msgID int64) error {
	return l.Store.Q.SetTgMenu(ctx, db.SetTgMenuParams{MenuMsgID: msgID, TgID: tgID})
}

func (l *Local) SetCurrent(ctx context.Context, tgID, userID int64) error {
	return l.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: tgID})
}

func (l *Local) UserOfCode(ctx context.Context, code string) (int64, error) {
	return l.Gateway.UserOfCode(ctx, code)
}

// UserOfAddress: the own token is a path segment of 24 letters and digits. An old panel's
// address is taken as the panel serves it (panelimport.LinkToken), and only while old links
// are set up; the signature of a token Marzban or PasarGuard signed is checked as there
// (Verifier.User).
func (l *Local) UserOfAddress(ctx context.Context, urlPath string) (int64, bool) {
	for seg := range strings.SplitSeq(urlPath, "/") {
		if !nativeToken.MatchString(seg) {
			continue
		}
		if u, err := l.Store.Q.GetUserBySubToken(ctx, seg); err == nil {
			return u.ID, true
		}
	}
	legacyPath, verifier, err := panelimport.LoadLegacy(ctx, l.Settings)
	if err != nil {
		return 0, false
	}
	token, _, ok := panelimport.LinkToken(urlPath, legacyPath)
	if !ok {
		return 0, false
	}
	if u, ok := verifier.User(ctx, l.Store.Q, token); ok {
		return u.ID, true
	}
	return 0, false
}

// Link: a subscription has one owner. An owner who cannot be asked, as they blocked the
// bot, is not in the way.
func (l *Local) Link(ctx context.Context, tgID, userID int64) (tgbot.Linked, error) {
	row, err := l.Store.Q.GetUser(ctx, userID)
	if err != nil {
		return tgbot.Linked{}, err
	}
	u := model.User(row)
	prev, err := l.Store.Q.GetTgLink(ctx, userID)
	switch {
	case err == nil && prev.TgID == tgID:
		_ = l.SetCurrent(ctx, tgID, userID)
		return tgbot.Linked{Status: tgbot.LinkAlready, User: u}, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return tgbot.Linked{}, err
	}
	// The check and the link are one transaction under the account's and the subscription's
	// locks (domain.LinkTg): two links at once do not pass the limit together, nor take a
	// subscription from each other unseen.
	owner := prev.TgID
	err = l.Store.TxRC(ctx, func(q *db.Queries) error {
		return domain.LinkTg(ctx, q, tgID, userID, model.MaxLinks, l.Now(), func(p int64) bool {
			owner = p
			return l.reachable(ctx, p)
		})
	})
	switch {
	case errors.Is(err, domain.ErrLinkLimit):
		return tgbot.Linked{Status: tgbot.LinkLimit, User: u}, nil
	case errors.Is(err, domain.ErrLinkTaken):
		return tgbot.Linked{Status: tgbot.LinkTaken, User: u, Owner: owner}, nil
	case err != nil:
		return tgbot.Linked{}, err
	}
	_ = l.SetCurrent(ctx, tgID, userID)
	return tgbot.Linked{Status: tgbot.LinkDone, User: u}, nil
}

// reachable: the account has a chat with the bot and has not blocked it, so it can be asked.
func (l *Local) reachable(ctx context.Context, tgID int64) bool {
	ch, err := l.Store.Q.GetTgChat(ctx, tgID)
	return err == nil && ch.Blocked == 0
}

func (l *Local) Owner(ctx context.Context, userID int64) (int64, error) {
	link, err := l.Store.Q.GetTgLink(ctx, userID)
	return link.TgID, err
}

func (l *Local) Transfer(ctx context.Context, userID, to int64) error {
	// The owner asked for it: whoever owned it lets it go (keep nil).
	err := l.Store.TxRC(ctx, func(q *db.Queries) error {
		return domain.LinkTg(ctx, q, to, userID, model.MaxLinks, l.Now(), nil)
	})
	if errors.Is(err, domain.ErrLinkLimit) {
		return model.ErrLinkLimit
	}
	if err != nil {
		return err
	}
	_ = l.SetCurrent(ctx, to, userID)
	return nil
}

func (l *Local) Unlink(ctx context.Context, userID int64) error {
	return l.Store.Q.UnlinkTg(ctx, userID)
}

func (l *Local) User(ctx context.Context, id int64) (model.User, error) {
	u, err := l.Store.Q.GetUser(ctx, id)
	return model.User(u), err
}

// GrantsLeft is what is left of the subscription's traffic packages, of the main traffic and
// of each group.
func (l *Local) GrantsLeft(ctx context.Context, userID int64, now time.Time) (model.GrantsLeft, error) {
	g, err := domain.UserGrantsLeft(ctx, l.Store.Q, userID, now)
	if err != nil {
		return model.GrantsLeft{}, err
	}
	out := model.GrantsLeft{Main: g.Main(userID)}
	for k, left := range g {
		if k[0] != userID || k[1] == 0 {
			continue
		}
		if out.Pools == nil {
			out.Pools = map[int64]int64{}
		}
		out.Pools[k[1]] = left
	}
	return out, nil
}

// Pools are the user's groups of their own limit, among the groups the user reaches, as the
// subscription's page shows them (domain.UserGroups: a group none of whose servers the user
// may use says nothing).
func (l *Local) Pools(ctx context.Context, userID int64, now time.Time) ([]tgbot.Pool, error) {
	q := l.Store.Q
	rows, err := q.ListUserPools(ctx, userID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	u, err := q.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	reach, err := domain.UserGroups(ctx, q, u)
	if err != nil {
		return nil, err
	}
	names, err := domain.PoolNames(ctx, q)
	if err != nil {
		return nil, err
	}
	grants, err := domain.UserGrantsLeft(ctx, q, userID, now)
	if err != nil {
		return nil, err
	}
	var out []tgbot.Pool
	for _, r := range rows {
		if r.Mode != domain.PoolOwn || !reach[r.PoolID] {
			continue // in the main traffic, without a limit, closed or out of reach: nothing to count down
		}
		extra := grants.Pool(userID, r.PoolID)
		out = append(out, tgbot.Pool{Name: names[r.PoolID], Used: r.UsedUp + r.UsedDown, Limit: r.TrafficLimit.Int64, Extra: extra,
			Exhausted: domain.PoolExhausted(r, extra)})
	}
	return out, nil
}

func (l *Local) DeviceBinding(ctx context.Context) bool {
	on, _ := l.Settings.On(ctx, settings.DeviceBinding)
	return on
}

func (l *Local) DeviceCount(ctx context.Context, userID int64) (int64, error) {
	return l.Store.Q.CountBoundDevices(ctx, userID)
}

func (l *Local) Devices(ctx context.Context, userID int64) ([]model.BoundDevice, error) {
	list, err := l.Store.Q.ListBoundDevices(ctx, userID)
	return model.Each(list, func(d db.BoundDevice) model.BoundDevice { return model.BoundDevice(d) }), err
}

func (l *Local) DeviceTraffic(ctx context.Context, userID int64) (map[int64]int64, error) {
	rows, err := l.Store.Q.ListBoundDeviceTraffic(ctx, userID)
	if err != nil {
		return nil, err
	}
	used := map[int64]int64{}
	for _, r := range rows {
		used[r.DeviceID] = r.Up + r.Down
	}
	return used, nil
}

func (l *Local) UnbindState(ctx context.Context, userID int64) (model.UnbindState, error) {
	s, err := l.Binder.UnbindState(ctx, userID)
	return s.Model(), err
}

func (l *Local) Unbind(ctx context.Context, userID, deviceID int64) error {
	return l.Binder.Unbind(ctx, userID, deviceID, true)
}

func (l *Local) ReissueState(ctx context.Context, userID int64) (model.ReissueState, error) {
	s, err := l.Users.ReissueState(ctx, userID)
	return s.Model(), err
}

func (l *Local) Reissue(ctx context.Context, userID int64) error {
	_, err := l.Users.ReissueByOwner(ctx, userID, "bot")
	return err
}

func (l *Local) Notices(ctx context.Context, now time.Time) ([]db.ListNoticeSubscriptionsRow, error) {
	return l.Store.Q.ListNoticeSubscriptions(ctx, now.Unix())
}

func (l *Local) NoticeSent(ctx context.Context, userID int64, kind string, period int64) (bool, error) {
	return l.Store.Q.TgNoticeSent(ctx, db.TgNoticeSentParams{UserID: userID, Kind: kind, Period: period})
}

func (l *Local) NoticeDelivered(ctx context.Context, userID int64, kind string, period int64, at time.Time) error {
	_, err := l.Store.Q.AddTgNotice(ctx, db.AddTgNoticeParams{UserID: userID, Kind: kind, Period: period, SentAt: at.Unix()})
	return err
}

func (l *Local) PruneNotices(ctx context.Context, before time.Time) error {
	return l.Store.Q.PruneTgNotices(ctx, before.Unix())
}

func (l *Local) BroadcastTargets(ctx context.Context) ([]int64, error) {
	return l.Store.Q.BroadcastTargets(ctx)
}

func (l *Local) Shop() tgbot.Shop { return shop{l.Billing} }

func (l *Local) Tariff(ctx context.Context, id int64) (model.Tariff, error) {
	t, err := l.Store.Q.GetTariff(ctx, id)
	return model.Tariff(t), err
}
