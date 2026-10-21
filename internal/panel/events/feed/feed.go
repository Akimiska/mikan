// Package feed writes the panel's feed of events (package events) in the caller's
// transaction and prunes it.
package feed

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"mikan/internal/panel/events"
	"mikan/internal/panel/store/db"
)

// emitLock is the advisory lock emitters take till their commit: ids are then committed in
// the order they were given, and a reader that saw id N never finds N-1 later.
const emitLock = 0x6d696b616e657631 // "mikanev1"

// Emit writes an event in the caller's transaction; dedupe, when given, makes a second
// emit of the same thing (a retried transaction, a notice scheduler run twice) a no-op.
func Emit(ctx context.Context, q *db.Queries, now time.Time, typ string, userID, tgID int64, data any, dedupe string) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := q.LockAddonEvents(ctx, emitLock); err != nil {
		return err
	}
	return q.AddAddonEvent(ctx, db.AddAddonEventParams{Type: typ, UserID: nullID(userID), TgID: nullID(tgID), Data: string(raw),
		DedupeKey: sql.NullString{String: dedupe, Valid: dedupe != ""}, CreatedAt: now.Unix()})
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

// Prune drops what is older than events.Retention.
func Prune(ctx context.Context, q *db.Queries, now time.Time) (int64, error) {
	return q.PruneAddonEvents(ctx, now.Add(-events.Retention).Unix())
}
