package model

import (
	"context"
	"database/sql"
	"errors"
)

// The errors the panel answers the bot with. Billing, domain and the gateway refuse with these
// very values, and each crosses the bot's RPC by its code (Codes) and comes back as itself, so
// errors.Is finds it on both sides of the RPC.
var (
	// ErrNotFound: no such subscription or tariff.
	ErrNotFound = errors.New("not found")
	// ErrLinkLimit: the account holds MaxLinks subscriptions already.
	ErrLinkLimit = errors.New("max_links")
	// ErrNotChannel: the channel check was asked about a chat that is not the bot's channel.
	ErrNotChannel = errors.New("not_the_channel")
	// ErrLinkInvalid and ErrLinkExpired: a link code the panel did not sign, or one too old.
	ErrLinkInvalid = errors.New("link_invalid")
	ErrLinkExpired = errors.New("link_expired")
	// ErrUnbindOff: the admin unbinds the subscriber's devices, the subscriber does not;
	// ErrUnbindCooldown: the subscriber used up the unbinds the admin's rules allow for now.
	ErrUnbindOff      = errors.New("unbind_off")
	ErrUnbindCooldown = errors.New("unbind_cooldown")
	// ErrReissueOff: the admin reissues links, the subscriber does not; ErrReissueCooldown:
	// the subscriber used up the reissues the admin's rules allow for now.
	ErrReissueOff      = errors.New("reissue_off")
	ErrReissueCooldown = errors.New("reissue_cooldown")
)

// The refusals of a purchase: why the buyer cannot have it (an invoice, a package, a device,
// the trial, the free plan, a top-up). The buyer's screens tell them by their text (Refusal).
var (
	ErrNotForSale  = errors.New("not_for_sale")
	ErrNewOff      = errors.New("new_off")
	ErrTooManySubs = errors.New("too_many_subs")
	ErrTooMany     = errors.New("too_many_invoices")
	ErrNotYours    = errors.New("not_yours")

	ErrTrialOff  = errors.New("trial_off")
	ErrTrialUsed = errors.New("trial_used")
	ErrTrialPaid = errors.New("trial_paid")

	ErrFreeOff     = errors.New("free_off")
	ErrFreeActive  = errors.New("free_active")
	ErrFreeChannel = errors.New("free_channel")

	ErrDevicesUnlimited = errors.New("devices_unlimited") // the subscription has no device limit
	ErrDevicesMax       = errors.New("devices_max")       // as many bought as the tariff allows

	ErrBalanceOff    = errors.New("balance_off")    // the balance is not on
	ErrBalanceLow    = errors.New("balance_low")    // not enough on the balance
	ErrTopupAmount   = errors.New("topup_amount")   // below the minimum or above the maximum
	ErrTopupMany     = errors.New("topup_many")     // as many top-ups wait already as may
	ErrTopupNone     = errors.New("topup_none")     // no top-up to send a screenshot for
	ErrTopupGone     = errors.New("topup_gone")     // decided already, or no such top-up
	ErrTopupCurrency = errors.New("topup_currency") // the balance's currency changed since

	// ErrProviderOff: no way to pay takes the purchase now.
	ErrProviderOff = errors.New("provider_off")
	// ErrPromo is any refusal of a promo code as it crosses to the bot: the buyer is told one
	// thing for all of them.
	ErrPromo = errors.New(RefusalPromo)
)

// RefusalPromo is the code of any refusal of a promo code.
const RefusalPromo = "promo_unavailable"

// refusals are the errors Refusal tells by their text. One table for the bot and the Mini App,
// so a new refusal is told the same way on both.
var refusals = []error{
	ErrNotForSale, ErrNewOff, ErrTooManySubs, ErrTooMany, ErrNotYours,
	ErrTrialUsed, ErrTrialOff, ErrTrialPaid, ErrFreeOff, ErrFreeActive, ErrFreeChannel,
	ErrDevicesUnlimited, ErrDevicesMax,
	ErrBalanceOff, ErrBalanceLow, ErrTopupAmount, ErrTopupMany, ErrTopupNone, ErrTopupGone, ErrTopupCurrency,
}

// Refusal is the code the buyer is told for err: the text of one of the refusals,
// RefusalPromo, or provider_off when no way to pay takes the purchase now. known is false for
// an error that is none of those (the provider failed, the panel did): that is told as
// "unavailable" and logged by the caller.
func Refusal(err error) (code string, known bool) {
	for _, e := range refusals {
		if errors.Is(err, e) {
			return e.Error(), true
		}
	}
	switch {
	case errors.Is(err, ErrPromo):
		return RefusalPromo, true
	case err == ErrProviderOff:
		// Wrapped, the provider failed while it was asked: not the buyer's to know.
		return ErrProviderOff.Error(), true
	}
	return "", false
}

// codes are the errors by the code they cross the bot's RPC with, in the order CodeOf asks.
// An older bot that does not know a code tells the error as "unavailable"; a code is never
// renamed or dropped.
var codes = []struct {
	code string
	err  error
}{
	{"no_rows", sql.ErrNoRows},
	{"not_found", ErrNotFound},
	{"link_limit", ErrLinkLimit},
	{"not_channel", ErrNotChannel},
	{"link_expired", ErrLinkExpired},
	{"link_invalid", ErrLinkInvalid},
	{"unbind_off", ErrUnbindOff},
	{"unbind_cooldown", ErrUnbindCooldown},
	{"reissue_off", ErrReissueOff},
	{"reissue_cooldown", ErrReissueCooldown},
	{"free_active", ErrFreeActive},
	{"free_channel", ErrFreeChannel},
	{"free_off", ErrFreeOff},
	{"not_for_sale", ErrNotForSale},
	{"not_yours", ErrNotYours},
	{"too_many", ErrTooMany},
	{"too_many_subs", ErrTooManySubs},
	{"trial_off", ErrTrialOff},
	{"trial_used", ErrTrialUsed},
	{"balance_off", ErrBalanceOff},
	{"balance_low", ErrBalanceLow},
	{"topup_amount", ErrTopupAmount},
	{"topup_many", ErrTopupMany},
	{"topup_none", ErrTopupNone},
	// Refusals a panel before 0.5.1.1 sent as a bare error.
	{"new_off", ErrNewOff},
	{"trial_paid", ErrTrialPaid},
	{"devices_unlimited", ErrDevicesUnlimited},
	{"devices_max", ErrDevicesMax},
	{"topup_gone", ErrTopupGone},
	{"topup_currency", ErrTopupCurrency},
	{"provider_off", ErrProviderOff},
	{"promo_unavailable", ErrPromo},
	{"context_canceled", context.Canceled},
	{"deadline_exceeded", context.DeadlineExceeded},
}

// CodeOf is the code err crosses the bot's RPC with, "" for an error that has none. A wrapped
// ErrProviderOff has none: the provider failed while it was asked (Refusal).
func CodeOf(err error) string {
	for _, c := range codes {
		if c.err == ErrProviderOff {
			if err == ErrProviderOff {
				return c.code
			}
			continue
		}
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	return ""
}

// ErrorOf is the error a code stands for; nil for a code there is no error of.
func ErrorOf(code string) error {
	for _, c := range codes {
		if c.code == code {
			return c.err
		}
	}
	return nil
}
