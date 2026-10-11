package billing

import (
	"context"
	"strconv"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// PaymentEvent is what the addons are told of a payment: enough to thank, notify or count,
// nothing of the provider's secrets.
type PaymentEvent struct {
	PaymentID int64  `json:"payment_id"`
	Kind      string `json:"kind"`     // new | renew | package | trial
	Provider  string `json:"provider"` // stars | addon:<id> | ...
	Amount    int64  `json:"amount"`   // minor units
	Currency  string `json:"currency"`
	TariffID  int64  `json:"tariff_id,omitempty"`
	PackageID int64  `json:"package_id,omitempty"`
	TermDays  int64  `json:"term_days,omitempty"`
	Created   bool   `json:"created,omitempty"` // the payment made the subscription
}

// emitPayment writes a payment's event in the caller's transaction, once per payment and type.
func emitPayment(ctx context.Context, q *db.Queries, now time.Time, typ string, p db.Payment, userID int64, created bool) error {
	return events.Emit(ctx, q, now, typ, userID, p.TgID, PaymentEvent{PaymentID: p.ID, Kind: p.Kind, Provider: p.Provider, Amount: p.Amount,
		Currency: p.Currency, TariffID: p.TariffID.Int64, PackageID: p.PackageID.Int64, TermDays: p.TermDays.Int64, Created: created},
		typ+":"+strconv.FormatInt(p.ID, 10))
}
