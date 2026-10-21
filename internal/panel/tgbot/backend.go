package tgbot

import (
	"context"
	"errors"
	"time"

	"mikan/internal/panel/model"
)

// The bot's screens, which run as the bot addon, see the panel through Backend: its data
// (who an account is, its subscriptions, what it may buy) and the channel check, in the
// panel's model (package model) alone, so the addon does not link the panel. Remote is that
// over the Panel API (docs/pipeline/addons.md, section 13), botpanel.Local the panel's side of
// the same calls, so the methods follow those endpoints. What the screens send goes through
// the Bot API proxy.

// Backend is the panel as the bot's screens use it.
type Backend interface {
	// Config is the admin's setup of the bot, or the default one in the panel's language
	// (GET /settings).
	Config(ctx context.Context) Config
	// Brand and SupportURL are the panel's settings as saved: "" when not set.
	Brand(ctx context.Context) string
	SupportURL(ctx context.Context) string
	// SubURL is u's subscription link; "" while the panel has no public address.
	SubURL(ctx context.Context, u model.User) string
	// MiniAppURL is where the Mini App opens; "" when Telegram could not load it (GET
	// /tg/mini-app).
	MiniAppURL(ctx context.Context) string
	// Member says whether tgID is in the bot's channel chat (GET /tg/subscribed). The
	// screens may ask about the bot's own channel only.
	Member(ctx context.Context, chat string, tgID int64, fresh bool) (bool, error)

	// Context is a Telegram account: its chat and the subscriptions linked to it (GET
	// /tg/context).
	Context(ctx context.Context, tgID int64) (Account, error)
	// Chat is the account's chat alone: its menu message, its current subscription and
	// whether it blocked the bot.
	Chat(ctx context.Context, tgID int64) (model.TgChat, error)
	// SetMenu and SetCurrent keep the chat's menu message and the subscription it shows.
	SetMenu(ctx context.Context, tgID, msgID int64) error
	SetCurrent(ctx context.Context, tgID, userID int64) error
	// UserOfCode is the subscription a /start code names (model.ErrLinkExpired, else
	// model.ErrLinkInvalid); UserOfAddress the one an address path leads to, the panel's own
	// or an old panel's (POST /tg/link/redeem).
	UserOfCode(ctx context.Context, code string) (int64, error)
	// TagCampaign makes the account an ad campaign's when it starts the bot by the
	// campaign's link with no subscription yet.
	TagCampaign(ctx context.Context, tgID int64, code string) (bool, error)
	UserOfAddress(ctx context.Context, urlPath string) (int64, bool)
	// Link ties subscription userID to account tgID, unless another account it can ask
	// owns it (LinkTaken) or tgID holds model.MaxLinks already.
	Link(ctx context.Context, tgID, userID int64) (Linked, error)
	// Owner is the account a subscription is linked to.
	Owner(ctx context.Context, userID int64) (int64, error)
	// Transfer moves subscription userID to account to and shows it there, its owner having
	// said yes: model.ErrLinkLimit when that account holds model.MaxLinks already (POST
	// /tg/transfer).
	Transfer(ctx context.Context, userID, to int64) error
	// Unlink takes a subscription off its account; the subscription stays (POST /tg/unlink).
	Unlink(ctx context.Context, userID int64) error

	// User is one subscription (GET /users/{id}).
	User(ctx context.Context, id int64) (model.User, error)
	// GrantsLeft is what is left of the traffic packages of a subscription.
	GrantsLeft(ctx context.Context, userID int64, now time.Time) (model.GrantsLeft, error)
	// Pools are the subscription's traffic pools with a limit.
	Pools(ctx context.Context, userID int64, now time.Time) ([]Pool, error)
	// DeviceBinding: subscribers' devices are bound (the panel's switch).
	DeviceBinding(ctx context.Context) bool
	// DeviceCount, Devices and DeviceTraffic are a subscription's bound devices (GET
	// /users/{id}/devices); DeviceTraffic is by device.
	DeviceCount(ctx context.Context, userID int64) (int64, error)
	Devices(ctx context.Context, userID int64) ([]model.BoundDevice, error)
	DeviceTraffic(ctx context.Context, userID int64) (map[int64]int64, error)
	// UnbindState and Unbind: the subscriber's own unbinding, under the panel's rules
	// (POST /users/{id}/devices/{dev}/unbind).
	UnbindState(ctx context.Context, userID int64) (model.UnbindState, error)
	Unbind(ctx context.Context, userID, deviceID int64) error
	// ReissueState and Reissue: the subscriber's own new link, under the panel's rules
	// (POST /users/{id}/reissue).
	ReissueState(ctx context.Context, userID int64) (model.ReissueState, error)
	Reissue(ctx context.Context, userID int64) error

	// Shop is what the panel sells in the bot (GET /offers, POST /billing/*).
	Shop() Shop
	// Tariff is one tariff, for what a trial, a free plan or an invoice gives.
	Tariff(ctx context.Context, id int64) (model.Tariff, error)
}

// Shop is the panel's billing as the bot's screens use it.
type Shop interface {
	Config(ctx context.Context) model.Config
	Offers(ctx context.Context) ([]model.Offer, model.Available, error)
	AddonName(ctx context.Context, id, lang string) string
	Invoice(ctx context.Context, req model.InvoiceRequest) (model.Payment, error)
	PackageOffers(ctx context.Context, userID int64) ([]model.PackageOffer, model.Available, error)
	PackageInvoice(ctx context.Context, req model.PackageRequest) (model.Payment, error)
	DeviceOfferFor(ctx context.Context, userID int64) (model.DeviceOffer, model.Available, bool, error)
	DeviceInvoice(ctx context.Context, req model.DeviceRequest) (model.Payment, error)
	TrialTariff(ctx context.Context) (model.Tariff, bool)
	TrialOpen(ctx context.Context, tgID int64) bool
	TrialAllowed(ctx context.Context, tgID int64) bool
	Trial(ctx context.Context, tgID int64) (model.User, error)
	TrialOffer(ctx context.Context, tgID int64) (model.TrialOffer, model.Available, bool)
	TrialInvoice(ctx context.Context, tgID int64, provider string) (model.Payment, error)
	FreeOffer(ctx context.Context, tgID int64) (model.FreeOffer, bool)
	FreePlan(ctx context.Context, tgID int64) (model.User, error)
	// The account's balance and its top-ups.
	BalanceOf(ctx context.Context, tgID int64) (int64, error)
	BalanceHistory(ctx context.Context, tgID int64, n int32) ([]model.BalanceOp, error)
	StartTopup(ctx context.Context, tgID, amount int64) (model.Topup, error)
	SetTopupAmount(ctx context.Context, tgID, amount int64) (model.Topup, error)
	TopupDraft(ctx context.Context, tgID int64) (model.Topup, bool)
	CancelTopup(ctx context.Context, tgID int64) error
	SubmitTopup(ctx context.Context, tgID int64, photo string) (model.Topup, error)
}

// Account is a Telegram account as the bot sees it.
type Account struct {
	Chat    model.TgChat // its chat; HasChat false before it wrote to the bot
	HasChat bool
	Links   []model.User // its subscriptions
}

// LinkStatus is how Backend.Link went.
type LinkStatus int

const (
	LinkDone    LinkStatus = iota // linked to the account and shown there
	LinkAlready                   // the account's already: shown there now
	LinkLimit                     // the account holds model.MaxLinks already
	LinkTaken                     // another account owns it and can be asked: Linked.Owner
)

// Linked is the subscription Backend.Link was about and what came of it.
type Linked struct {
	Status LinkStatus
	User   model.User
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

// ErrNotTalking: the Panel API refused a call about an account that is not talking to the
// bot now (the addon reaches only those; Remote only).
var ErrNotTalking = errors.New("not_talking")

var _ Backend = (*Remote)(nil)
