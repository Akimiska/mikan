package nodesync

import (
	"context"
	"database/sql"
	"slices"

	"mikan/internal/panel/subs"
	"mikan/internal/panel/torrent"
)

// AoiVPN fork: the torrent blocker only for users whose apps ignore the subscription's
// rules. A user whose every bound device fetches it with a rule-capable app (AoiVPN and
// other mihomo clients) sends torrents DIRECT by the profile's own rule and is exempt; one
// device on v2rayNG or Happ, which take links only, keeps the user under the blocker. A
// user with no device known yet is not exempt. The node cannot tell the apps apart — they
// share the user's keys — so the choice is per user.

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// withRuleClientsExempt adds the users with only rule-capable apps to cfg.Exempt.
func withRuleClientsExempt(ctx context.Context, q queryer, cfg torrent.Config) (torrent.Config, error) {
	if !cfg.Enabled {
		return cfg, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT user_id, app FROM bound_devices`)
	if err != nil {
		return cfg, err
	}
	defer rows.Close()
	capable := map[int64]bool{}
	for rows.Next() {
		var uid int64
		var app string
		if err := rows.Scan(&uid, &app); err != nil {
			return cfg, err
		}
		ok, seen := capable[uid]
		capable[uid] = subs.RuleCapable(app) && (ok || !seen)
	}
	if err := rows.Err(); err != nil {
		return cfg, err
	}
	exempt := slices.Clone(cfg.Exempt)
	for uid, ok := range capable {
		if ok && !slices.Contains(exempt, uid) {
			exempt = append(exempt, uid)
		}
	}
	slices.Sort(exempt)
	cfg.Exempt = exempt
	return cfg, nil
}
