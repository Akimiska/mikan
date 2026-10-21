package model

import (
	"database/sql"
	"math"
	"regexp"
	"slices"
	"strings"
)

// The shop as billing has it (internal/panel/billing): what the admin set up, the tariffs on
// sale with their prices, and what a buyer asks for. The types are billing's field for field
// (billing converts to them: Config.Model, Offer.Model, ...), with their JSON as the bot's RPC
// carries it.

// Stars is Telegram's own currency; every other provider is a marketplace adapter,
// "addon:<id>", or the account's balance.
const Stars = "stars"

// Balance is the payment method of the account's balance.
const Balance = "balance"

// AddonPrefix starts the provider name of a payment made through an adapter.
const AddonPrefix = "addon:"

// validAddonID: adapter ids are path and provider-name safe.
var validAddonID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidAddonID says whether id is an adapter's id as the marketplace names them.
func ValidAddonID(id string) bool { return validAddonID.MatchString(id) }

// AddonID is the adapter of a provider name, "" for a built-in provider or a name that is no
// adapter's.
func AddonID(provider string) string {
	id, ok := strings.CutPrefix(provider, AddonPrefix)
	if !ok || !ValidAddonID(id) {
		return ""
	}
	return id
}

// The kinds of payment besides a new subscription ("new") and a renewal ("renew").
const (
	KindPackage = "package"
	KindDevice  = "device"
	KindTrial   = "trial"
)

// Top-up states; a draft is one whose screenshot has not come yet.
const (
	TopupDraft    = "draft"
	TopupPending  = "pending"
	TopupApproved = "approved"
	TopupRejected = "rejected"
)

// Kinds of balance operations.
const (
	OpTopup  = "topup"
	OpSpend  = "spend"
	OpRefund = "refund"
	OpAdjust = "adjust"
)

// What the free plan asks of an account (Config.FreeCondition).
const (
	FreeNoCondition = ""
	FreeChannel     = "channel" // a subscription to the bot's channel
)

// Config is what the admin sets for selling.
type Config struct {
	// Enabled is the switch for selling at all: off, the bot and the Mini App offer
	// nothing and take no new invoices, while invoices already opened are still applied.
	Enabled bool `json:"enabled"`
	Stars   bool `json:"stars"`
	// AllowNew lets people without a subscription buy one; off: only renewals.
	AllowNew bool `json:"allow_new"`
	// RenewResetsTraffic: a paid renewal also starts a new traffic period.
	RenewResetsTraffic bool `json:"renew_resets_traffic"`
	// TrialTariffID is the tariff of the trial a Telegram account may take once; 0: none.
	TrialTariffID int64 `json:"trial_tariff_id,omitempty"`
	// TrialStars and TrialRub (kopecks) put a price on the trial; both 0: it is free.
	TrialStars int64 `json:"trial_stars,omitempty"`
	TrialRub   int64 `json:"trial_rub,omitempty"`
	// FreeTariffID is the tariff of the free plan, taken again once the last one ended; 0:
	// no free plan.
	FreeTariffID int64 `json:"free_tariff_id,omitempty"`
	// FreeCondition is what the free plan asks of an account: FreeNoCondition or FreeChannel.
	FreeCondition string `json:"free_condition,omitempty"`
	// ExtraDevicesKeep: devices bought on top of the tariff stay with renewals, which are
	// then priced with them; off, they last until the term ends.
	ExtraDevicesKeep bool `json:"extra_devices_keep,omitempty"`
	// Balance is the accounts' internal balance.
	Balance BalanceConfig `json:"balance"`
}

// TrialPaid: the trial has a price and is bought, not taken.
func (c Config) TrialPaid() bool { return c.TrialStars > 0 || c.TrialRub > 0 }

// BalanceCurrencies are the currencies a balance may be kept in.
var BalanceCurrencies = []string{"RUB", "USD", "EUR", "UAH", "BYN", "KZT", "UZS", "USDT"}

// BalanceConfig is the admin's setup of the accounts' balance.
type BalanceConfig struct {
	Enabled bool `json:"enabled"`
	// Currency is the balance's (BalanceCurrencies); Rate is how many rubles one unit of it
	// is worth, for the tariffs' ruble prices: 1 for RUB.
	Currency string  `json:"currency"`
	Rate     float64 `json:"rate"`
	// Details is how to pay, shown before the screenshot is asked: a card, a wallet.
	Details string `json:"details"`
	// Min and Max bound one top-up, in minor units; 0: no bound.
	Min int64 `json:"min,omitempty"`
	Max int64 `json:"max,omitempty"`
}

// On: the balance is switched on and set up well enough to take payments.
func (c BalanceConfig) On() bool {
	return c.Enabled && slices.Contains(BalanceCurrencies, c.Currency) && (c.Currency == "RUB" || c.Rate > 0) && strings.TrimSpace(c.Details) != ""
}

// FromRub is a ruble price (kopecks) in the balance's minor units, rounded up.
func (c BalanceConfig) FromRub(kopecks int64) int64 {
	if c.Currency == "RUB" || c.Rate <= 0 {
		return kopecks
	}
	return int64(math.Ceil(float64(kopecks) / c.Rate))
}

// Available says which providers can take a payment right now: selling on, Stars with the
// bot running, and the adapters that are on, set up and running.
type Available struct {
	Stars bool
	// Addons are the adapters that take rubles now, by id.
	Addons []string
	// Balance pays at the ruble prices, in its own currency; zero while it is off.
	Balance BalanceConfig
}

// Any: some provider takes a payment now.
func (a Available) Any() bool { return a.Stars || a.Rub() }

// Rub: some provider takes rubles (the balance counts: it pays the ruble prices).
func (a Available) Rub() bool { return len(a.Addons) > 0 || a.Balance.On() }

// Offer is a tariff on sale with the terms a buyer can pay for now. Stars and Rub are those
// of the first of Terms.
type Offer struct {
	Tariff Tariff
	Terms  []OfferTerm // at least one
	First  int64       // days of the tariff's first term, which Terms may not hold
	Stars  int64       // 0: not for Stars
	Rub    int64       // kopecks; 0: not for rubles
	// Groups are the server groups with servers in them, as the tariff has each; Gifts the
	// packages it gives with every payment. Both say what the tariff is made of.
	Groups []OfferGroup
	Gifts  []OfferGift
}

// Term is the offer's term of so many days; nil days: the tariff's first term, the one an
// invoice without days is for (not there when no available provider takes its price).
func (o Offer) Term(days *int64) (OfferTerm, bool) {
	first := o.First
	if days == nil {
		days = &first
	}
	for _, t := range o.Terms {
		if t.Days == *days {
			return t, true
		}
	}
	return OfferTerm{}, false
}

// OfferTerm is a term of a tariff on sale with the prices the available providers take.
type OfferTerm struct {
	Days  int64
	Stars int64 // 0: not for Stars
	Rub   int64 // kopecks; 0: not for rubles
}

// OfferGroup is a server group of a tariff on sale, as the bot and the Mini App list it.
type OfferGroup struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`            // PoolMain, PoolOwn, PoolUnlimited, PoolClosed
	Limit int64  `json:"limit,omitempty"` // bytes, with PoolOwn
}

// OfferGift is a traffic package a tariff on sale gives with every payment.
type OfferGift struct {
	Package TrafficPackage
	Pool    string // the group's name; "" for the main traffic
}

// PackageOffer is a traffic package a subscription can buy now, with its prices.
type PackageOffer struct {
	Package TrafficPackage
	Pool    string // the pool's name; "" for the main traffic
	Stars   int64  // 0: not for Stars
	Rub     int64  // kopecks; 0: not for rubles
}

// DeviceOffer is one more device a subscription can buy now.
type DeviceOffer struct {
	Limit  int64 // the places the subscription has now
	Extra  int64 // how many of them were bought
	Max    int64 // how many the tariff lets buy
	Stars  int64
	Rub    int64 // kopecks
	Keep   bool  // they stay with renewals; else until ExpiresAt
	Expiry sql.NullInt64
}

// TrialOffer is the priced trial an account can buy.
type TrialOffer struct {
	Tariff Tariff
	Stars  int64
	Rub    int64 // kopecks
}

// FreeOffer is the free plan an account can take, and the channel it asks to join ("" for
// none).
type FreeOffer struct {
	Tariff  Tariff
	Channel string
}

// InvoiceRequest: who buys which tariff for which term with what. UserID 0 buys a new
// subscription.
type InvoiceRequest struct {
	TgID      int64
	UserID    int64
	TariffID  int64
	TermDays  *int64 // the term's days; nil: the tariff's first term
	Provider  string
	PromoCode string
}

// PackageRequest: who buys which package for which subscription with what.
type PackageRequest struct {
	TgID      int64
	UserID    int64
	PackageID int64
	Provider  string
	PromoCode string
}

// DeviceRequest: who buys one more device for which subscription with what.
type DeviceRequest struct {
	TgID     int64
	UserID   int64
	Provider string
}

// RenewalExtras is what a renewal of u onto t costs on top of the term's prices, in Stars and
// in rubles, when bought devices stay with renewals: their price each, per term. An invoice
// adds them to the prices before it converts them for the way to pay, so a screen that adds
// them the same way shows what is charged.
func RenewalExtras(cfg Config, u User, t Tariff) (stars, rub int64) {
	if !cfg.ExtraDevicesKeep || u.ExtraDevices <= 0 || !u.TariffID.Valid || u.TariffID.Int64 != t.ID {
		return 0, 0
	}
	if t.DevicePriceStars.Valid {
		stars = t.DevicePriceStars.Int64 * u.ExtraDevices
	}
	if t.DevicePriceRub.Valid {
		rub = t.DevicePriceRub.Int64 * u.ExtraDevices
	}
	return stars, rub
}
