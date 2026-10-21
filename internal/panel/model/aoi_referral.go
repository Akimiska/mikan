package model

import "errors"

// AoiVPN fork: the referral programme (internal/panel/aoiref) as the bot sees it. Kept here,
// not in errors.go, so a merge of upstream never meets it.

// ReferralPrefix starts the /start parameter of a referral link: /start ref-<code>.
const ReferralPrefix = "ref-"

// ErrReferralInvalid: no account has this referral code.
var ErrReferralInvalid = errors.New("referral_invalid")

func init() {
	codes = append(codes, struct {
		code string
		err  error
	}{"referral_invalid", ErrReferralInvalid})
}

// ReferralStats is an account's referral programme as the bot shows it.
type ReferralStats struct {
	Code     string
	Link     string
	Invited  int64 // referrals made
	Paid     int64 // of them, paid (rewarded or waiting for the hold)
	Rewarded int64
	Days     int64 // days earned
	Bank     int64 // days waiting for a subscription
}
