package domain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"mikan/internal/panel/store/db"
)

// Gifts of a tariff: traffic packages its buyer gets with every payment for it, a purchase
// or a renewal, each as a grant of that payment. A gift adds to a quota the tariff gives:
// the main traffic when it has a limit, or a server group with a limit of its own.

// GiftOf is what package p gives as a gift of payment paymentID.
func GiftOf(p db.TrafficPackage, paymentID int64) GrantSpec {
	return GrantSpec{PoolID: p.PoolID.Int64, Bytes: p.Bytes, Lifetime: p.Lifetime, Days: p.Days, Source: SourceGift, PaymentID: paymentID, PackageID: p.ID}
}

// TariffGifts are the packages each tariff gives, by tariff id.
func TariffGifts(ctx context.Context, q *db.Queries) (map[int64][]int64, error) {
	rows, err := q.ListAllTariffGifts(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64][]int64{}
	for _, r := range rows {
		out[r.TariffID] = append(out[r.TariffID], r.PackageID)
	}
	return out, nil
}

// SetTariffGifts makes ids the gifts of tariff t on q's transaction, after its groups are
// written: each a package of the catalog, not archived, that adds to a quota the tariff
// gives. A list that names one that is not is refused whole.
func SetTariffGifts(ctx context.Context, q *db.Queries, t db.Tariff, ids []int64) error {
	if err := q.ClearTariffGifts(ctx, t.ID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	pools, err := q.ListTariffPools(ctx, t.ID)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		p, err := q.GetTrafficPackage(ctx, id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fieldErr("gifts", "package_not_found")
		case err != nil:
			return err
		case p.Archived != 0:
			return fieldErr("gifts", "package_archived")
		case !PackageForTariff(t, pools, p):
			return fieldErr("gifts", "gift_not_fit")
		}
		if err := q.AddTariffGift(ctx, db.AddTariffGiftParams{TariffID: t.ID, PackageID: id}); err != nil {
			return err
		}
	}
	return nil
}

// GrantGifts gives u the gifts of tariff tariffID for payment paymentID on q's transaction,
// after the payment applied the tariff. A gift that no longer adds to a quota of the
// subscription (the tariff changed since it was chosen) is left out: its traffic would
// wait in a quota nobody spends.
func GrantGifts(ctx context.Context, q *db.Queries, u db.User, tariffID, paymentID int64, now time.Time) ([]db.TrafficGrant, error) {
	gifts, err := q.ListTariffGifts(ctx, tariffID)
	if err != nil || len(gifts) == 0 {
		return nil, err
	}
	pools, err := q.ListUserPools(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	var out []db.TrafficGrant
	for _, gf := range gifts {
		p, err := q.GetTrafficPackage(ctx, gf.PackageID)
		if err != nil {
			return out, err
		}
		if p.Archived != 0 || !PackageFits(u, pools, p, nil) {
			continue
		}
		g, err := GrantTx(ctx, q, u, GiftOf(p, paymentID), now)
		if err != nil {
			return out, err
		}
		out = append(out, g)
	}
	return out, nil
}
