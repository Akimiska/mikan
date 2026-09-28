package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"mikan/internal/panel/store/db"
)

type Entry struct {
	AdminID    int64 // 0 = system or CLI
	Action     string
	TargetType string
	TargetID   string
	IP         string
	Details    any // must not contain secrets
}

func Write(ctx context.Context, q *db.Queries, now time.Time, e Entry) error {
	var details sql.NullString
	if e.Details != nil {
		raw, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		details = sql.NullString{String: string(raw), Valid: true}
	}
	return q.InsertAudit(ctx, db.InsertAuditParams{
		Ts:         now.Unix(),
		AdminID:    sql.NullInt64{Int64: e.AdminID, Valid: e.AdminID != 0},
		Action:     e.Action,
		TargetType: nullString(e.TargetType),
		TargetID:   nullString(e.TargetID),
		Ip:         nullString(e.IP),
		Details:    details,
	})
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
