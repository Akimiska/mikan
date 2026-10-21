package billing

import "context"

// AoiVPN fork: the AoiVPN app's purchases (internal/panel/aoiapp) send the buyer back to
// the app's return page instead of the bot.

type returnURLKey struct{}

// WithReturnURL makes the invoices opened with ctx send the buyer to url after paying.
func WithReturnURL(ctx context.Context, url string) context.Context {
	return context.WithValue(ctx, returnURLKey{}, url)
}

func returnURL(ctx context.Context) string {
	u, _ := ctx.Value(returnURLKey{}).(string)
	return u
}

// aoiAddonName (AoiVPN fork) is what a payment adapter is called in the shop: the way to pay
// the buyer knows ("Картой, СБП или SberPay"), not the processor's brand.
func aoiAddonName(id, lang string) string {
	if id != "platega" {
		return ""
	}
	if lang == "en" {
		return "Card, SBP or SberPay"
	}
	return "Картой, СБП или SberPay"
}
