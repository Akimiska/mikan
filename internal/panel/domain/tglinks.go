package domain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"mikan/internal/panel/store/db"
)

var (
	// ErrLinkLimit: the account holds as many subscriptions as one may.
	ErrLinkLimit = errors.New("link_limit")
	// ErrLinkTaken: another account owns the subscription, and the caller's rule keeps it.
	ErrLinkTaken = errors.New("link_taken")
)

// LinkTg makes Telegram account tgID the owner of subscription userID on q's transaction:
// the account's links and the subscription are locked first, so that two links at once
// neither pass the account's limit (max) together nor take one subscription from each other
// unseen. keep says whether the owner before (prev, not 0 nor tgID) keeps it: ErrLinkTaken
// then; nil lets any owner go. An account that owns it already changes nothing.
func LinkTg(ctx context.Context, q *db.Queries, tgID, userID int64, max int, now time.Time, keep func(prev int64) bool) error {
	if err := q.LockTgLinks(ctx, tgID); err != nil {
		return err
	}
	if locked, err := q.LockUsers(ctx, []int64{userID}); err != nil {
		return err
	} else if len(locked) == 0 {
		return ErrNotFound
	}
	prev, err := q.GetTgLink(ctx, userID)
	switch {
	case err == nil && prev.TgID == tgID:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if n, err := q.CountTgLinksOf(ctx, tgID); err != nil {
		return err
	} else if n >= int64(max) {
		return ErrLinkLimit
	}
	if prev.TgID != 0 && keep != nil && keep(prev.TgID) {
		return ErrLinkTaken
	}
	return q.LinkTg(ctx, db.LinkTgParams{UserID: userID, TgID: tgID, CreatedAt: now.Unix()})
}
