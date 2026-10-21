package billing

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/model"
	"mikan/internal/panel/promo"
	"mikan/internal/panel/secure"
	"mikan/internal/panel/store/db"
)

// Traffic packages (GitHub issue #12) are sold to a subscription's owner like a renewal:
// the payment names the package and the subscription, and applying it gives the
// subscription a grant on the same transaction that marks the payment applied.

// KindPackage is a payment for a traffic package.
const KindPackage = model.KindPackage

var (
	errUserGone    = errors.New("user_gone")    // the subscription was deleted after the invoice
	errPackageGone = errors.New("package_gone") // the package's pool was deleted after the invoice
)

// PackageOffer is a package on sale for one subscription, with the prices the available
// providers take.
type PackageOffer struct {
	Package db.TrafficPackage
	Pool    string // the pool's name; "" for the main traffic
	Stars   int64  // 0: not for Stars
	Rub     int64  // kopecks; 0: not for rubles
}

// PackageOffers lists the packages the subscription's owner can pay for now: on sale,
// for a quota the subscription has and can spend (domain.PackagesFor), with a price a
// provider takes.
func (s *Service) PackageOffers(ctx context.Context, userID int64) ([]PackageOffer, Available, error) {
	av := s.Available(ctx)
	if !av.Any() {
		return nil, av, nil
	}
	q := s.d.Store.Q
	ps, err := domain.PackagesFor(ctx, q, userID, s.d.Now())
	if err != nil {
		return nil, av, err
	}
	names, err := domain.PoolNames(ctx, q)
	if err != nil {
		return nil, av, err
	}
	var out []PackageOffer
	for _, p := range ps {
		o := PackageOffer{Package: p, Pool: names[p.PoolID.Int64]}
		o.Stars, o.Rub = av.prices(p.PriceStars, p.PriceRub)
		if o.Stars > 0 || o.Rub > 0 {
			out = append(out, o)
		}
	}
	return out, av, nil
}

// PackageRequest: the owner of subscription UserID buys a package with Provider.
type PackageRequest struct {
	TgID      int64
	UserID    int64
	PackageID int64
	Provider  string
	PromoCode string
}

// PackageInvoice opens a payment for a package. Only the subscription's owner buys for it;
// an open invoice for the same purchase made in the last minutes is returned again.
func (s *Service) PackageInvoice(ctx context.Context, req PackageRequest) (db.Payment, error) {
	if err := s.promoGuard(ctx, req.Provider, req.PromoCode); err != nil {
		return db.Payment{}, err
	}
	q := s.d.Store.Q
	if link, err := q.GetTgLink(ctx, req.UserID); err != nil || link.TgID != req.TgID {
		return db.Payment{}, ErrNotYours
	}
	offers, av, err := s.PackageOffers(ctx, req.UserID)
	if err != nil {
		return db.Payment{}, err
	}
	var offer *PackageOffer
	for i := range offers {
		if offers[i].Package.ID == req.PackageID {
			offer = &offers[i]
		}
	}
	if offer == nil {
		return db.Payment{}, ErrNotForSale
	}
	p := offer.Package
	user, err := q.GetUser(ctx, req.UserID)
	if err != nil {
		return db.Payment{}, err
	}
	amount, currency, ok := av.price(req.Provider, p.PriceStars, p.PriceRub)
	if !ok {
		return db.Payment{}, ErrProviderOff
	}
	unlock, err := s.lockBuyer(ctx, req.TgID)
	if err != nil {
		return db.Payment{}, err
	}
	defer unlock()
	now := s.d.Now()
	userID := sql.NullInt64{Int64: req.UserID, Valid: true}
	packageID := sql.NullInt64{Int64: p.ID, Valid: true}
	pay, open, err := s.newPayment(ctx, req.TgID, now,
		func(q *db.Queries) ([]db.Payment, error) {
			return q.FindOpenPackagePayments(ctx, db.FindOpenPackagePaymentsParams{TgID: req.TgID, PackageID: packageID, Provider: req.Provider, UserID: userID,
				Since: now.Add(-invoiceReuse).Unix(), RepeatSince: now.Add(-balanceRepeat).Unix()})
		},
		func(q *db.Queries, existing db.Payment) (bool, error) {
			return s.matchesOpenPayment(ctx, q, existing, amount, req.PromoCode)
		},
		func(q *db.Queries) (db.Payment, error) {
			created, err := q.CreatePackagePayment(ctx, db.CreatePackagePaymentParams{Provider: req.Provider, Payload: secure.Token(32), TgID: req.TgID, UserID: userID, PackageID: packageID, TariffName: p.Name, Amount: amount, Currency: currency, CreatedAt: now.Unix()})
			if err != nil {
				return db.Payment{}, err
			}
			// A code is tied to tariffs: a subscription without one takes none.
			if s.d.Promo != nil && strings.TrimSpace(req.PromoCode) != "" && !user.TariffID.Valid {
				return db.Payment{}, promo.ErrTariff
			}
			if err := s.discount(ctx, q, &created, req.UserID, user.TariffID.Int64, req.PromoCode); err != nil {
				return db.Payment{}, err
			}
			return created, nil
		})
	if err != nil {
		return db.Payment{}, err
	}
	if open {
		return pay, nil
	}
	lang, _ := s.d.Settings.Lang(ctx)
	return s.openPayment(ctx, pay, p.Name, model.DescribePackage(model.TrafficPackage(p), offer.Pool, lang))
}

// packageOnSale: the package of a payment can still be bought (Telegram's pre-checkout),
// by the rule the offers were made with (domain.PackagesFor): what changed since the
// invoice (the group's mode, the tariff, the subscription's term) is not paid for.
func (s *Service) packageOnSale(ctx context.Context, pay db.Payment) error {
	if !pay.PackageID.Valid || !pay.UserID.Valid {
		return ErrNotForSale
	}
	ps, err := domain.PackagesFor(ctx, s.d.Store.Q, pay.UserID.Int64, s.d.Now())
	if errors.Is(err, domain.ErrNotFound) {
		return ErrNotForSale
	}
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(ps, func(p db.TrafficPackage) bool { return p.ID == pay.PackageID.Int64 }) {
		return ErrNotForSale
	}
	return nil
}

// applyPackage gives the paid package to the payment's subscription on q's transaction.
// A package archived or taken off sale since the invoice still applies: it is paid for.
// So does one for a pool closed on the tariff since then (the provider took the money, and
// refusing it here would lose it): the grant waits in the pool until the pool is opened
// again or the grant expires, and the warning tells the operator to refund or reopen.
func (s *Service) applyPackage(ctx context.Context, q *db.Queries, pay db.Payment) (db.User, error) {
	if !pay.UserID.Valid {
		return db.User{}, errUserGone
	}
	if !pay.PackageID.Valid {
		return db.User{}, errPackageGone
	}
	u, err := q.GetUser(ctx, pay.UserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errUserGone
	}
	if err != nil {
		return u, err
	}
	p, err := q.GetTrafficPackage(ctx, pay.PackageID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errPackageGone
	}
	if err != nil {
		return u, err
	}
	if pools, err := q.ListUserPools(ctx, u.ID); err == nil && domain.PackagePoolClosed(pools, p) {
		s.d.Log.Warn("billing: a paid package is for a pool closed for the subscription", "payment", pay.ID, "user", u.ID, "package", p.ID, "pool", p.PoolID.Int64)
	}
	_, err = domain.GrantTx(ctx, q, u, domain.GrantOf(p, pay.ID), s.d.Now())
	return u, err
}
