package nodesync

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/events"
	"mikan/internal/panel/events/feed"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/subs"
	"mikan/internal/panel/torrent"
)

// AoiVPN fork: the torrent blocker only for users whose apps ignore the subscription's
// rules. A user whose every app seen in the last week (bound_devices) is rule-capable
// (AoiVPN and other mihomo clients) sends torrents DIRECT by the profile's own rule and is
// exempt; one app on v2rayNG, Happ or INCY, which take links only, keeps the user under
// the blocker. Browsers, curl and link-preview bots are not apps and do not count; a user
// with no app seen is not exempt. The node cannot tell the apps apart — they share the
// user's keys — so the choice is per user.

// ruleClientsWindow is how far back an app counts: an app the user left keeps no say.
const ruleClientsWindow = 7 * 24 * time.Hour

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// withRuleClientsExempt adds the users with only rule-capable apps to cfg.Exempt.
func withRuleClientsExempt(ctx context.Context, q queryer, cfg torrent.Config) (torrent.Config, error) {
	if !cfg.Enabled {
		return cfg, nil
	}
	since := time.Now().Add(-ruleClientsWindow).Unix()
	rows, err := q.QueryContext(ctx, `SELECT user_id, app FROM bound_devices WHERE last_seen > $1`, since)
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
		client, rules := subs.ClientRules(app)
		if !client {
			continue
		}
		ok, seen := capable[uid]
		capable[uid] = rules && (ok || !seen)
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

// emitTorrentBan (AoiVPN fork) tells the bot addon that a catch banned the user, so the
// subscriber hears why every server went quiet. One word an hour at most per user: a client
// that keeps seeding is caught again and again, the message must not come with each catch.
// A user with no Telegram account linked has nobody to tell.
func emitTorrentBan(ctx context.Context, q *db.Queries, now time.Time, uid, until, minutes int64, h nodeapi.TorrentHit) error {
	link, err := q.TgLinkOfUser(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	dedupe := "torrent:" + strconv.FormatInt(uid, 10) + ":" + strconv.FormatInt(now.Unix()/3600, 10)
	return feed.Emit(ctx, q, now, events.TorrentBanned, uid, link.TgID,
		events.TorrentBan{Until: until, Minutes: minutes, Kind: h.Kind, Inbound: h.Inbound}, dedupe)
}
