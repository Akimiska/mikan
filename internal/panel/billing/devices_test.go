package billing

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// deviceEnv: buyer 555's subscription on the sale tariff (3 devices), which sells up to
// two more devices at 40 Stars each.
func deviceEnv(t *testing.T) (*env, db.User) {
	t.Helper()
	e := newEnv(t)
	ctx := context.Background()
	u, err := e.s.d.Users.Create(ctx, domain.CreateInput{Name: "mine", TariffID: e.sale.ID})
	must(t, err)
	must(t, e.st.Q.LinkTg(ctx, db.LinkTgParams{UserID: u.ID, TgID: 555, CreatedAt: 1}))
	must(t, e.st.Q.SetTariffExtraDevices(ctx, db.SetTariffExtraDevicesParams{DevicePriceStars: sql.NullInt64{Int64: 40, Valid: true}, DeviceExtraMax: 2, ID: e.sale.ID}))
	return e, u
}

func (e *env) buyDevice(user int64, charge string) db.Payment {
	e.t.Helper()
	ctx := context.Background()
	p, err := e.s.DeviceInvoice(ctx, DeviceRequest{TgID: 555, UserID: user, Provider: Stars})
	must(e.t, err)
	must(e.t, e.s.PreCheckout(ctx, 555, p.Payload, "XTR", p.Amount))
	must(e.t, e.s.StarsPaid(ctx, 555, p.Payload, charge, "XTR", p.Amount))
	return e.payment(p.ID)
}

// A device bought until the term ends: the limit grows by one up to the tariff's cap, a
// refund takes it back, and a renewal starts again from the tariff's limit.
func TestExtraDeviceForTheTerm(t *testing.T) {
	e, u := deviceEnv(t)
	ctx := context.Background()
	o, _, ok, err := e.s.DeviceOfferFor(ctx, u.ID)
	if err != nil || !ok || o.Limit != 3 || o.Stars != 40 || o.Keep {
		t.Fatalf("offer: %+v %v %v", o, ok, err)
	}
	if _, err := e.s.DeviceInvoice(ctx, DeviceRequest{TgID: 556, UserID: u.ID, Provider: Stars}); !errors.Is(err, ErrNotYours) {
		t.Fatalf("someone else's subscription: %v", err)
	}
	p := e.buyDevice(u.ID, "ch-d1")
	if p.Status != "applied" || p.Kind != KindDevice || p.Amount != 40 {
		t.Fatalf("payment: %+v", p)
	}
	got, _ := e.st.Q.GetUser(ctx, u.ID)
	if got.DeviceLimit.Int64 != 4 || got.ExtraDevices != 1 || got.ExtraDevicesUntil != u.ExpiresAt {
		t.Fatalf("after one: limit %v extra %d until %v (term %v)", got.DeviceLimit, got.ExtraDevices, got.ExtraDevicesUntil, u.ExpiresAt)
	}
	second := e.buyDevice(u.ID, "ch-d2")
	if _, _, ok, err := e.s.DeviceOfferFor(ctx, u.ID); ok || !errors.Is(err, ErrDevicesMax) {
		t.Fatalf("over the cap: %v %v", ok, err)
	}
	if rv, err := e.s.Refund(ctx, second.ID); err != nil || rv.Action != "device" {
		t.Fatalf("refund: %+v %v", rv, err)
	}
	got, _ = e.st.Q.GetUser(ctx, u.ID)
	if got.DeviceLimit.Int64 != 4 || got.ExtraDevices != 1 {
		t.Fatalf("after the refund: %v %d", got.DeviceLimit, got.ExtraDevices)
	}

	renew := e.invoice(555, u.ID, Stars)
	if renew.Amount != 150 {
		t.Fatalf("a renewal costs the term alone: %d", renew.Amount)
	}
	must(t, e.s.PreCheckout(ctx, 555, renew.Payload, "XTR", renew.Amount))
	must(t, e.s.StarsPaid(ctx, 555, renew.Payload, "ch-r", "XTR", renew.Amount))
	got, _ = e.st.Q.GetUser(ctx, u.ID)
	if got.DeviceLimit.Int64 != 3 || got.ExtraDevices != 0 {
		t.Fatalf("the new term starts from the tariff: %v %d", got.DeviceLimit, got.ExtraDevices)
	}
}

// Devices that stay with renewals: bought without an end, priced into every renewal and
// kept by it.
func TestExtraDeviceKeptWithRenewals(t *testing.T) {
	e, u := deviceEnv(t)
	ctx := context.Background()
	must(t, settings.Set(ctx, e.s.d.Settings, KeyConfig, Config{Enabled: true, Stars: true, AllowNew: true, RenewResetsTraffic: true, ExtraDevicesKeep: true}))
	e.buyDevice(u.ID, "ch-k1")
	got, _ := e.st.Q.GetUser(ctx, u.ID)
	if got.DeviceLimit.Int64 != 4 || got.ExtraDevices != 1 || got.ExtraDevicesUntil.Valid {
		t.Fatalf("bought: %v %d %v", got.DeviceLimit, got.ExtraDevices, got.ExtraDevicesUntil)
	}
	renew := e.invoice(555, u.ID, Stars)
	if renew.Amount != 190 {
		t.Fatalf("a renewal with the device costs 150 + 40: %d", renew.Amount)
	}
	must(t, e.s.PreCheckout(ctx, 555, renew.Payload, "XTR", renew.Amount))
	must(t, e.s.StarsPaid(ctx, 555, renew.Payload, "ch-kr", "XTR", renew.Amount))
	got, _ = e.st.Q.GetUser(ctx, u.ID)
	if got.DeviceLimit.Int64 != 4 || got.ExtraDevices != 1 {
		t.Fatalf("the renewal keeps the device: %v %d", got.DeviceLimit, got.ExtraDevices)
	}
}

// A subscription without a device limit has nothing to buy; nor one whose tariff sells no
// devices.
func TestExtraDeviceNotOffered(t *testing.T) {
	e, u := deviceEnv(t)
	ctx := context.Background()
	must(t, e.st.Q.SetTariffExtraDevices(ctx, db.SetTariffExtraDevicesParams{ID: e.sale.ID}))
	if _, _, ok, err := e.s.DeviceOfferFor(ctx, u.ID); ok || err != nil {
		t.Fatalf("no price, no offer: %v %v", ok, err)
	}
	must(t, e.st.Q.SetTariffExtraDevices(ctx, db.SetTariffExtraDevicesParams{DevicePriceStars: sql.NullInt64{Int64: 40, Valid: true}, DeviceExtraMax: 2, ID: e.sale.ID}))
	_, err := e.st.DB.ExecContext(ctx, "UPDATE users SET device_limit = NULL WHERE id = $1", u.ID)
	must(t, err)
	if _, _, ok, err := e.s.DeviceOfferFor(ctx, u.ID); ok || !errors.Is(err, ErrDevicesUnlimited) {
		t.Fatalf("unlimited: %v %v", ok, err)
	}
}
