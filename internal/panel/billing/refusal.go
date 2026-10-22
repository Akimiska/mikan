package billing

import (
	"mikan/internal/panel/model"
	"mikan/internal/panel/promo"
)

// Refusal is the code the buyer is told for err: a refusal of the purchase (model.Refusal),
// or model.RefusalPromo for any refusal of a promo code. known is false for an error that is
// none of those (the provider failed, the panel did): that is told as "unavailable" and
// logged by the caller.
func Refusal(err error) (code string, known bool) {
	if code, known := model.Refusal(err); known {
		return code, true
	}
	if promo.IsRefusal(err) {
		return model.RefusalPromo, true
	}
	return "", false
}
