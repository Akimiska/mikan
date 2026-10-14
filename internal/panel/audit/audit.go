package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
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

// Actor is how a line names who acted when it was not an admin: Details["actor"]. An admin
// is AdminID; the system and the console leave both empty.
const (
	ActorSubscriber = "subscriber"
	actorAddon      = "addon:"
	actorSupport    = "support:"
)

// AddonActor is the actor of a line written for an addon acting on a permission the admin
// approved: "addon:<id>". It is what tells such a line from the admin's own.
func AddonActor(id string) string { return actorAddon + id }

// ActorSupport is the actor of a line written for a support operator acting on a grant the
// admin opened: "support:<grant id>". The journal's lines about the grant itself are
// support.grant, support.enter, support.revoke and support.expire (target_type
// support_grant, target_id the grant id).
func ActorSupport(grantID int64) string { return actorSupport + strconv.FormatInt(grantID, 10) }

// TargetSupportGrant is the target_type of the support.* lines.
const TargetSupportGrant = "support_grant"

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
