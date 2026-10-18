package billing

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// The internal balance of a Telegram account. The account tops it up by hand: it says how
// much, transfers the money the way the admin wrote (Config.Balance.Details) and sends a
// screenshot; the admin approves or rejects it in the panel. The balance then pays like any
// payment method ("balance"): subscriptions, renewals, packages and devices, at the tariff's
// ruble price in the balance's currency (Rate). A refund of such a payment goes back to it.

// Balance is the payment method of the balance.
const Balance = "balance"

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

// maxPendingTopups an account may have waiting for the admin at once.
const maxPendingTopups = 3

var (
	ErrBalanceOff  = errors.New("balance_off")  // the balance is not on
	ErrBalanceLow  = errors.New("balance_low")  // not enough on the balance
	ErrTopupAmount = errors.New("topup_amount") // below the minimum or above the maximum
	ErrTopupMany   = errors.New("topup_many")   // maxPendingTopups wait already
	ErrTopupNone   = errors.New("topup_none")   // no top-up to send a screenshot for
	ErrTopupGone   = errors.New("topup_gone")   // decided already, or no such top-up
)

// BalanceCurrencies are the currencies a balance may be kept in.
var BalanceCurrencies = []string{"RUB", "USD", "EUR", "UAH", "BYN", "KZT", "UZS", "USDT"}

// BalanceConfig is the admin's setup of the balance (Config.Balance).
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

// On: the balance takes top-ups and pays.
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

// BalanceOf is the account's balance now, in minor units of the balance's currency.
func (s *Service) BalanceOf(ctx context.Context, tgID int64) (int64, error) {
	n, err := s.d.Store.Q.GetBalance(ctx, tgID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// BalanceHistory is the account's last n operations, newest first.
func (s *Service) BalanceHistory(ctx context.Context, tgID int64, n int32) ([]db.BalanceOp, error) {
	return s.d.Store.Q.ListBalanceOps(ctx, db.ListBalanceOpsParams{TgID: tgID, Limit: n})
}

// chargeBalance pays p from its buyer's balance: the money leaves the balance and the
// payment is paid on one transaction, then applied as any paid payment (a failure to apply
// is retried by Reconcile, the money stays taken). ErrBalanceLow when there is not enough.
func (s *Service) chargeBalance(ctx context.Context, p db.Payment) error {
	now := s.d.Now().Unix()
	err := s.d.Store.Tx(ctx, func(q *db.Queries) error {
		n, err := q.DebitBalance(ctx, db.DebitBalanceParams{Amount: p.Amount, UpdatedAt: now, TgID: p.TgID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrBalanceLow
		}
		if err := q.AddBalanceOp(ctx, db.AddBalanceOpParams{TgID: p.TgID, Amount: -p.Amount, Kind: OpSpend, PaymentID: sql.NullInt64{Int64: p.ID, Valid: true},
			Note: p.TariffName, CreatedAt: now}); err != nil {
			return err
		}
		m, err := q.MarkPaymentPaid(ctx, db.MarkPaymentPaidParams{ExternalID: sql.NullString{String: "balance:" + strconv.FormatInt(p.ID, 10), Valid: true},
			PaidAt: sql.NullInt64{Int64: now, Valid: true}, ID: p.ID})
		if err != nil {
			return err
		}
		if m != 1 {
			return ErrBadPayment
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.d.Log.Info("billing: paid from the balance", "payment", p.ID, "amount", p.Amount, "currency", p.Currency)
	return s.Apply(ctx, p.ID)
}

// refundToBalance puts a refunded balance payment back on the balance, once, on the
// transaction that marks it refunded.
func refundToBalance(ctx context.Context, q *db.Queries, p db.Payment, now int64) error {
	if p.Provider != Balance || !p.PaidAt.Valid {
		return nil
	}
	if _, err := q.BalanceOpOfPayment(ctx, db.BalanceOpOfPaymentParams{PaymentID: sql.NullInt64{Int64: p.ID, Valid: true}, Kind: OpRefund}); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := q.CreditBalance(ctx, db.CreditBalanceParams{TgID: p.TgID, Amount: p.Amount, UpdatedAt: now}); err != nil {
		return err
	}
	return q.AddBalanceOp(ctx, db.AddBalanceOpParams{TgID: p.TgID, Amount: p.Amount, Kind: OpRefund, PaymentID: sql.NullInt64{Int64: p.ID, Valid: true},
		Note: p.TariffName, CreatedAt: now})
}

// topupAmountOK: amount is within the admin's bounds.
func (c BalanceConfig) topupAmountOK(amount int64) bool {
	return amount > 0 && amount <= maxTopup && (c.Min == 0 || amount >= c.Min) && (c.Max == 0 || amount <= c.Max)
}

// maxTopup bounds any top-up (minor units), whatever the admin's Max.
const maxTopup = 1_000_000_000

// StartTopup opens the account's top-up of amount (minor units): a draft until its
// screenshot comes (SubmitTopup); amount 0 is a draft that waits for its amount
// (SetTopupAmount). An earlier draft is dropped.
func (s *Service) StartTopup(ctx context.Context, tgID, amount int64) (db.Topup, error) {
	c := s.Config(ctx).Balance
	if !c.On() {
		return db.Topup{}, ErrBalanceOff
	}
	if amount != 0 && !c.topupAmountOK(amount) {
		return db.Topup{}, ErrTopupAmount
	}
	var t db.Topup
	err := s.d.Store.Tx(ctx, func(q *db.Queries) error {
		if n, err := q.PendingTopupsOf(ctx, tgID); err != nil {
			return err
		} else if n >= maxPendingTopups {
			return ErrTopupMany
		}
		if err := q.DropDraftTopups(ctx, tgID); err != nil {
			return err
		}
		var err error
		t, err = q.CreateTopup(ctx, db.CreateTopupParams{TgID: tgID, Amount: amount, Currency: c.Currency, CreatedAt: s.d.Now().Unix()})
		return err
	})
	return t, err
}

// SetTopupAmount gives the account's draft its amount.
func (s *Service) SetTopupAmount(ctx context.Context, tgID, amount int64) (db.Topup, error) {
	c := s.Config(ctx).Balance
	if !c.On() {
		return db.Topup{}, ErrBalanceOff
	}
	if !c.topupAmountOK(amount) {
		return db.Topup{}, ErrTopupAmount
	}
	q := s.d.Store.Q
	t, err := q.DraftTopupOf(ctx, tgID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Topup{}, ErrTopupNone
	}
	if err != nil {
		return db.Topup{}, err
	}
	if n, err := q.SetTopupAmount(ctx, db.SetTopupAmountParams{Amount: amount, ID: t.ID}); err != nil {
		return db.Topup{}, err
	} else if n == 0 {
		return db.Topup{}, ErrTopupNone
	}
	t.Amount = amount
	return t, nil
}

// TopupDraft is the account's top-up waiting for its amount or its screenshot, if any.
func (s *Service) TopupDraft(ctx context.Context, tgID int64) (db.Topup, bool) {
	t, err := s.d.Store.Q.DraftTopupOf(ctx, tgID)
	return t, err == nil
}

// CancelTopup drops the account's draft.
func (s *Service) CancelTopup(ctx context.Context, tgID int64) error {
	return s.d.Store.Q.DropDraftTopups(ctx, tgID)
}

// SubmitTopup gives the account's draft its screenshot (a Telegram file id): it waits for
// the admin now.
func (s *Service) SubmitTopup(ctx context.Context, tgID int64, photo string) (db.Topup, error) {
	if photo == "" || len(photo) > 256 {
		return db.Topup{}, ErrTopupNone
	}
	q := s.d.Store.Q
	t, err := q.DraftTopupOf(ctx, tgID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Topup{}, ErrTopupNone
	}
	if err != nil {
		return db.Topup{}, err
	}
	n, err := q.SubmitTopup(ctx, db.SubmitTopupParams{Photo: photo, CreatedAt: s.d.Now().Unix(), ID: t.ID})
	if err != nil {
		return db.Topup{}, err
	}
	if n == 0 {
		return db.Topup{}, ErrTopupNone
	}
	t.Photo, t.Status = photo, TopupPending
	if n := s.d.TopupWaiting; n != nil {
		n(context.WithoutCancel(ctx), t)
	}
	return t, nil
}

// DecideTopup approves (the amount goes on the balance) or rejects a pending top-up, once:
// a second decision finds it decided (ErrTopupGone). The account is told (BalanceTopup).
func (s *Service) DecideTopup(ctx context.Context, id int64, approve bool, adminID int64, reason string) (db.Topup, int64, error) {
	var (
		t       db.Topup
		balance int64
	)
	status := TopupRejected
	if approve {
		status = TopupApproved
	}
	now := s.d.Now()
	err := s.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		if t, err = q.GetTopup(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrTopupGone
		} else if err != nil {
			return err
		}
		n, err := q.DecideTopup(ctx, db.DecideTopupParams{Status: status, DecidedAt: sql.NullInt64{Int64: now.Unix(), Valid: true},
			AdminID: sql.NullInt64{Int64: adminID, Valid: adminID != 0}, Reason: reason, ID: id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrTopupGone
		}
		t.Status, t.Reason = status, reason
		if approve {
			if balance, err = q.CreditBalance(ctx, db.CreditBalanceParams{TgID: t.TgID, Amount: t.Amount, UpdatedAt: now.Unix()}); err != nil {
				return err
			}
			if err := q.AddBalanceOp(ctx, db.AddBalanceOpParams{TgID: t.TgID, Amount: t.Amount, Kind: OpTopup, TopupID: sql.NullInt64{Int64: id, Valid: true},
				AdminID: sql.NullInt64{Int64: adminID, Valid: adminID != 0}, CreatedAt: now.Unix()}); err != nil {
				return err
			}
		} else if balance, err = q.GetBalance(ctx, t.TgID); errors.Is(err, sql.ErrNoRows) {
			balance = 0
		} else if err != nil {
			return err
		}
		return events.Emit(ctx, q, now, events.BalanceTopup, 0, t.TgID, events.Topup{TopupID: id, Status: status, Amount: t.Amount, Currency: t.Currency,
			Balance: balance, Reason: reason}, events.BalanceTopup+":"+strconv.FormatInt(id, 10))
	})
	if err != nil {
		return db.Topup{}, 0, err
	}
	s.d.Events.Kick()
	return t, balance, nil
}

// AdjustBalance changes an account's balance by delta at the admin's hand (a correction, a
// gift); it never goes below zero. It returns the balance after.
func (s *Service) AdjustBalance(ctx context.Context, tgID, delta, adminID int64, note string) (int64, error) {
	if delta == 0 {
		return s.BalanceOf(ctx, tgID)
	}
	now := s.d.Now().Unix()
	var balance int64
	err := s.d.Store.Tx(ctx, func(q *db.Queries) error {
		var err error
		if delta > 0 {
			if balance, err = q.CreditBalance(ctx, db.CreditBalanceParams{TgID: tgID, Amount: delta, UpdatedAt: now}); err != nil {
				return err
			}
		} else {
			n, err := q.DebitBalance(ctx, db.DebitBalanceParams{Amount: -delta, UpdatedAt: now, TgID: tgID})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrBalanceLow
			}
			if balance, err = q.GetBalance(ctx, tgID); err != nil {
				return err
			}
		}
		return q.AddBalanceOp(ctx, db.AddBalanceOpParams{TgID: tgID, Amount: delta, Kind: OpAdjust, AdminID: sql.NullInt64{Int64: adminID, Valid: adminID != 0},
			Note: note, CreatedAt: now})
	})
	return balance, err
}
