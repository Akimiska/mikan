package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Extra devices: the owner of a subscription buys one more place in the bot or the Mini
// App without renewing. The tariff puts the price on a place and caps how many may be
// bought. Config.ExtraDevicesKeep says how long they last: until the term ends (off), or
// on with every renewal, which is then priced with them (on).

// KindDevice is a payment for one more device.
const KindDevice = "device"

var (
	ErrDevicesUnlimited = errors.New("devices_unlimited") // the subscription has no device limit
	ErrDevicesMax       = errors.New("devices_max")       // as many bought as the tariff allows
)

// DeviceOffer is one more device for a subscription, with the prices the available
// providers take.
type DeviceOffer struct {
	Limit  int64 // the places the subscription has now
	Extra  int64 // how many of them were bought
	Max    int64 // how many the tariff lets buy
	Stars  int64
	Rub    int64 // kopecks
	Keep   bool  // they stay with renewals; else until ExpiresAt
	Expiry sql.NullInt64
}

// DeviceOfferFor says whether the owner of userID may buy one more device now, and for
// how much. ok is false when there is nothing to offer: selling off, no price on the
// tariff, no limit, or the cap reached (err tells which of the last two).
func (s *Service) DeviceOfferFor(ctx context.Context, userID int64) (o DeviceOffer, av Available, ok bool, err error) {
	av = s.Available(ctx)
	if !av.Any() {
		return o, av, false, nil
	}
	q := s.d.Store.Q
	u, err := q.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return o, av, false, errUserGone
	}
	if err != nil || !u.TariffID.Valid || u.Status != "active" {
		return o, av, false, err
	}
	if u.ExpiresAt.Valid && store.Time(u.ExpiresAt.Int64).Before(s.d.Now()) {
		return o, av, false, nil
	}
	t, err := q.GetTariff(ctx, u.TariffID.Int64)
	if err != nil {
		return o, av, false, err
	}
	o = DeviceOffer{Extra: u.ExtraDevices, Max: t.DeviceExtraMax, Keep: s.Config(ctx).ExtraDevicesKeep, Expiry: u.ExpiresAt}
	o.Stars, o.Rub = av.prices(t.DevicePriceStars, t.DevicePriceRub)
	switch {
	case o.Stars == 0 && o.Rub == 0, t.DeviceExtraMax <= 0:
		return o, av, false, nil
	case !u.DeviceLimit.Valid:
		return o, av, false, ErrDevicesUnlimited
	case u.ExtraDevices >= t.DeviceExtraMax:
		o.Limit = u.DeviceLimit.Int64
		return o, av, false, ErrDevicesMax
	}
	o.Limit = u.DeviceLimit.Int64
	return o, av, true, nil
}

// DeviceRequest: the owner of subscription UserID buys one more device with Provider.
type DeviceRequest struct {
	TgID     int64
	UserID   int64
	Provider string
}

// DeviceInvoice opens a payment for one more device. Only the subscription's owner buys
// for it; an open invoice for the same purchase made in the last minutes is returned again.
// Promo codes do not apply to it.
func (s *Service) DeviceInvoice(ctx context.Context, req DeviceRequest) (db.Payment, error) {
	q := s.d.Store.Q
	if link, err := q.GetTgLink(ctx, req.UserID); err != nil || link.TgID != req.TgID {
		return db.Payment{}, ErrNotYours
	}
	o, av, ok, err := s.DeviceOfferFor(ctx, req.UserID)
	if err != nil {
		return db.Payment{}, err
	}
	if !ok {
		return db.Payment{}, ErrNotForSale
	}
	u, err := q.GetUser(ctx, req.UserID)
	if err != nil {
		return db.Payment{}, err
	}
	t, err := q.GetTariff(ctx, u.TariffID.Int64)
	if err != nil {
		return db.Payment{}, err
	}
	amount, currency, ok := av.price(req.Provider, t.DevicePriceStars, t.DevicePriceRub)
	if !ok {
		return db.Payment{}, ErrProviderOff
	}
	unlock, err := s.lockBuyer(ctx, req.TgID)
	if err != nil {
		return db.Payment{}, err
	}
	defer unlock()
	lang, _ := s.d.Settings.Lang(ctx)
	title := DescribeDevice(lang)
	now := s.d.Now()
	userID := sql.NullInt64{Int64: req.UserID, Valid: true}
	tariffID := sql.NullInt64{Int64: t.ID, Valid: true}
	pay, open, err := s.newPayment(ctx, req.TgID, now,
		func(q *db.Queries) ([]db.Payment, error) {
			return q.FindOpenPayments(ctx, db.FindOpenPaymentsParams{TgID: req.TgID, TariffID: tariffID, Provider: req.Provider, Kind: KindDevice,
				UserID: req.UserID, Since: now.Add(-invoiceReuse).Unix()})
		},
		func(q *db.Queries, existing db.Payment) (bool, error) { return existing.Amount == amount, nil },
		func(q *db.Queries) (db.Payment, error) {
			return q.CreatePayment(ctx, db.CreatePaymentParams{Provider: req.Provider, Payload: secure.Token(32), TgID: req.TgID, Kind: KindDevice,
				UserID: userID, TariffID: tariffID, TariffName: title, Amount: amount, Currency: currency, CreatedAt: now.Unix()})
		})
	if err != nil || open {
		return pay, err
	}
	return s.openPayment(ctx, pay, title, DescribeDeviceOffer(o, lang))
}

// DescribeDevice names the purchase: "+1 device".
func DescribeDevice(lang string) string {
	if lang == "en" {
		return "+1 device"
	}
	return "+1 устройство"
}

// DescribeDeviceOffer says what one more device gives and for how long.
func DescribeDeviceOffer(o DeviceOffer, lang string) string {
	en := lang == "en"
	switch {
	case o.Keep && en:
		return fmt.Sprintf("One more device for the subscription: %d in all. It stays with renewals.", o.Limit+1)
	case o.Keep:
		return fmt.Sprintf("Ещё одно устройство к подписке: всего %d. Остаётся при продлении.", o.Limit+1)
	case en:
		return fmt.Sprintf("One more device for the subscription: %d in all, until the term ends.", o.Limit+1)
	}
	return fmt.Sprintf("Ещё одно устройство к подписке: всего %d, до конца срока.", o.Limit+1)
}

// deviceOnSale: the device of a payment can still be bought (Telegram's pre-checkout).
func (s *Service) deviceOnSale(ctx context.Context, pay db.Payment) error {
	if !pay.UserID.Valid {
		return ErrNotForSale
	}
	if _, _, ok, err := s.DeviceOfferFor(ctx, pay.UserID.Int64); err != nil || !ok {
		return ErrNotForSale
	}
	return nil
}

// applyDevice gives the paid device to the payment's subscription on q's transaction. A
// device bought over the cap (two invoices paid at once) still applies: it is paid for.
func (s *Service) applyDevice(ctx context.Context, q *db.Queries, pay db.Payment) (db.User, error) {
	if !pay.UserID.Valid {
		return db.User{}, errUserGone
	}
	u, err := q.GetUser(ctx, pay.UserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errUserGone
	}
	if err != nil {
		return u, err
	}
	until := u.ExpiresAt
	if cfg, err := s.LoadConfig(ctx); err != nil {
		return u, err
	} else if cfg.ExtraDevicesKeep {
		until = sql.NullInt64{}
	}
	return q.AddUserExtraDevice(ctx, db.AddUserExtraDeviceParams{Until: until, UpdatedAt: s.d.Now().Unix(), ID: u.ID})
}

// renewalExtra is what a renewal of u costs on top of its term when bought devices stay
// with renewals: their price each, per term.
func renewalExtra(cfg Config, u db.User, t db.Tariff, av Available, provider string) int64 {
	if !cfg.ExtraDevicesKeep || u.ExtraDevices <= 0 || !u.TariffID.Valid || u.TariffID.Int64 != t.ID {
		return 0
	}
	each, _, ok := av.price(provider, t.DevicePriceStars, t.DevicePriceRub)
	if !ok {
		return 0
	}
	return each * u.ExtraDevices
}
