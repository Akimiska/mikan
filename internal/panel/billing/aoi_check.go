package billing

import (
	"context"

	"mikan/internal/panel/model"
)

// CheckPendingNow asks the payment's adapter about it right away (AoiVPN fork): the app
// polls its order while the buyer pays, and without a webhook (Platega sends none to us)
// the paid invoice was only seen on the next minute's Reconcile — the renewal showed up
// 40–120 s late. A paid answer is recorded and applied the same way a webhook would.
func (s *Service) CheckPendingNow(ctx context.Context, paymentID int64) error {
	pay, err := s.d.Store.Q.GetPayment(ctx, paymentID)
	if err != nil {
		return err
	}
	if pay.Status != PaymentPending || model.AddonID(pay.Provider) == "" || !pay.ExternalID.Valid {
		return nil
	}
	return s.checkAddonPayment(ctx, pay)
}
