package tgbot

import (
	"context"
	"errors"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/tgate"
)

// The bot's screens see the panel through two interfaces: Backend, the panel's data (who
// an account is, its subscriptions, what it may buy), and Transport, the Telegram gateway
// it sends through. Local and *tgate.Gateway are the in-process ones; the bot addon gets
// the same over the Panel API and the Bot API proxy (docs/pipeline/addons.md, sections 6
// and 7), so the methods follow those endpoints.

// Backend is the panel as the bot's screens use it.
type Backend interface {
	// Config is the admin's setup of the bot, or the default one in the panel's language
	// (GET /settings).
	Config(ctx context.Context) Config
	// Brand and SupportURL are the panel's settings as saved: "" when not set.
	Brand(ctx context.Context) string
	SupportURL(ctx context.Context) string
	// SubURL is u's subscription link; "" while the panel has no public address.
	SubURL(ctx context.Context, u db.User) string
	// MiniAppURL is where the Mini App opens; "" when Telegram could not load it (GET
	// /tg/mini-app).
	MiniAppURL(ctx context.Context) string

	// Context is a Telegram account: its chat and the subscriptions linked to it (GET
	// /tg/context).
	Context(ctx context.Context, tgID int64) (Account, error)
	// Chat is the account's chat alone: its menu message, its current subscription and
	// whether it blocked the bot.
	Chat(ctx context.Context, tgID int64) (db.TgChat, error)
	// SetMenu and SetCurrent keep the chat's menu message and the subscription it shows.
	SetMenu(ctx context.Context, tgID, msgID int64) error
	SetCurrent(ctx context.Context, tgID, userID int64) error
	// UserOfCode is the subscription a /start code names (tgate.ErrLinkExpired, else
	// tgate.ErrLinkInvalid); UserOfAddress the one an address path leads to, the panel's own
	// or an old panel's (POST /tg/link/redeem).
	UserOfCode(ctx context.Context, code string) (int64, error)
	UserOfAddress(ctx context.Context, urlPath string) (int64, bool)
	// Link ties subscription userID to account tgID, unless another account it can ask
	// owns it (LinkTaken) or tgID holds MaxLinks already.
	Link(ctx context.Context, tgID, userID int64) (Linked, error)
	// Owner is the account a subscription is linked to.
	Owner(ctx context.Context, userID int64) (int64, error)
	// Transfer moves subscription userID to account to and shows it there, its owner having
	// said yes: ErrLinkLimit when that account holds MaxLinks already (POST /tg/transfer).
	Transfer(ctx context.Context, userID, to int64) error
	// Unlink takes a subscription off its account; the subscription stays (POST /tg/unlink).
	Unlink(ctx context.Context, userID int64) error

	// User is one subscription (GET /users/{id}).
	User(ctx context.Context, id int64) (db.User, error)
	// GrantsLeft is what is left of the traffic packages of a subscription.
	GrantsLeft(ctx context.Context, userID int64, now time.Time) (domain.GrantsLeft, error)
	// Pools are the subscription's traffic pools with a limit.
	Pools(ctx context.Context, userID int64, now time.Time) ([]Pool, error)
	// DeviceBinding: subscribers' devices are bound (the panel's switch).
	DeviceBinding(ctx context.Context) bool
	// DeviceCount, Devices and DeviceTraffic are a subscription's bound devices (GET
	// /users/{id}/devices); DeviceTraffic is by device.
	DeviceCount(ctx context.Context, userID int64) (int64, error)
	Devices(ctx context.Context, userID int64) ([]db.BoundDevice, error)
	DeviceTraffic(ctx context.Context, userID int64) (map[int64]int64, error)
	// UnbindState and Unbind: the subscriber's own unbinding, under the panel's rules
	// (POST /users/{id}/devices/{dev}/unbind).
	UnbindState(ctx context.Context, userID int64) (domain.UnbindState, error)
	Unbind(ctx context.Context, userID, deviceID int64) error
	// ReissueState and Reissue: the subscriber's own new link, under the panel's rules
	// (POST /users/{id}/reissue); ErrUnavailable where the panel does not offer it.
	ReissueState(ctx context.Context, userID int64) (domain.ReissueState, error)
	Reissue(ctx context.Context, userID int64) error

	// The notices about the term and the traffic: the subscriptions that may get one, and
	// which were delivered (tg_notices). They move into the panel's events later.
	Notices(ctx context.Context, now time.Time) ([]db.ListNoticeSubscriptionsRow, error)
	NoticeSent(ctx context.Context, userID int64, kind string, period int64) (bool, error)
	NoticeDelivered(ctx context.Context, userID int64, kind string, period int64, at time.Time) error
	PruneNotices(ctx context.Context, before time.Time) error
	// BroadcastTargets are the accounts a broadcast goes to.
	BroadcastTargets(ctx context.Context) ([]int64, error)

	// Shop is what the panel sells in the bot; nil while it sells nothing (GET /offers,
	// POST /billing/*).
	Shop() Shop
	// Tariff is one tariff, for what a trial, a free plan or an invoice gives.
	Tariff(ctx context.Context, id int64) (db.Tariff, error)
}

// Shop is the panel's billing as the bot's screens use it (*billing.Service).
type Shop interface {
	Config(ctx context.Context) billing.Config
	Offers(ctx context.Context) ([]billing.Offer, billing.Available, error)
	AddonName(ctx context.Context, id, lang string) string
	Invoice(ctx context.Context, req billing.InvoiceRequest) (db.Payment, error)
	PackageOffers(ctx context.Context, userID int64) ([]billing.PackageOffer, billing.Available, error)
	PackageInvoice(ctx context.Context, req billing.PackageRequest) (db.Payment, error)
	TrialTariff(ctx context.Context) (db.Tariff, bool)
	TrialOpen(ctx context.Context, tgID int64) bool
	TrialAllowed(ctx context.Context, tgID int64) bool
	Trial(ctx context.Context, tgID int64) (db.User, error)
	TrialOffer(ctx context.Context, tgID int64) (billing.TrialOffer, billing.Available, bool)
	TrialInvoice(ctx context.Context, tgID int64, provider string) (db.Payment, error)
	FreeOffer(ctx context.Context, tgID int64) (billing.FreeOffer, bool)
	FreePlan(ctx context.Context, tgID int64) (db.User, error)
}

// Transport is the Telegram gateway as the bot's screens use it (*tgate.Gateway); the
// updates come the other way, through tgate.Handler.
type Transport interface {
	// Outbox is the running bot's outbox; nil while the bot is off.
	Outbox() *tgate.Outbox
	// Go runs f as work the gateway's Run waits for.
	Go(f func())
	// Member says whether tgID is in the channel chat (GET /tg/subscribed).
	Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error)
}

// Account is a Telegram account as the bot sees it.
type Account struct {
	Chat    db.TgChat // its chat; HasChat false before it wrote to the bot
	HasChat bool
	Links   []db.User // its subscriptions
}

// LinkStatus is how Backend.Link went.
type LinkStatus int

const (
	LinkDone    LinkStatus = iota // linked to the account and shown there
	LinkAlready                   // the account's already: shown there now
	LinkLimit                     // the account holds MaxLinks already
	LinkTaken                     // another account owns it and can be asked: Linked.Owner
)

// Linked is the subscription Backend.Link was about and what came of it.
type Linked struct {
	Status LinkStatus
	User   db.User
	Owner  int64
}

// Pool is a traffic pool of a subscription, with its limit.
type Pool struct {
	Name      string
	Used      int64
	Limit     int64
	Extra     int64 // left of the pool's packages
	Exhausted bool
}

var (
	// ErrLinkLimit: the account holds MaxLinks subscriptions already.
	ErrLinkLimit = errors.New("max_links")
	// ErrUnavailable: the panel does not offer this.
	ErrUnavailable = errors.New("unavailable")
)

// The in-process pieces are what the panel wires together (app/panel.go).
var (
	_ Backend          = (*Local)(nil)
	_ Shop             = (*billing.Service)(nil)
	_ Transport        = (*tgate.Gateway)(nil)
	_ tgate.Handler    = (*Bot)(nil)
	_ billing.Notifier = (*Bot)(nil)
	_ billing.Telegram = (*tgate.Gateway)(nil)
)
