package nodesync

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/store/db"
)

// torrentPullEvery is how often a node is asked for its torrent blocker's hits. The node
// keeps a caught user out for a couple of minutes by itself; the ban reaches the other
// nodes within this and a policy push.
const torrentPullEvery = 5 * time.Second

// torrentKeep is how long the hits are kept; a ban still running is kept until it ends.
const torrentKeep = 90 * 24 * time.Hour

type torrentSource interface {
	Torrents(ctx context.Context, epoch string, after int64) (nodeapi.TorrentHits, error)
}

// pullTorrents stores the node's new torrent hits. A caught user is banned on every node
// from the panel's clock, so a node's wrong clock cannot make a ban longer or shorter.
func (s *Syncer) pullTorrents(ctx context.Context) {
	snap, err := s.m.snapshot(ctx, s.id)
	if err != nil || !snap.torrent.Enabled {
		return
	}
	src, ok := s.node.(torrentSource)
	if !ok {
		return
	}
	epoch, seq, err := s.torrentPos(ctx)
	if err != nil {
		s.log.Error("torrent position", "err", err)
		return
	}
	got, err := src.Torrents(ctx, epoch, seq)
	if err != nil {
		return // a node older than the blocker, or one that is down: the health shows which
	}
	if got.Epoch != epoch {
		seq = 0
	}
	var hits []nodeapi.TorrentHit
	last := seq
	for _, h := range got.Hits {
		if h.Seq > seq {
			hits = append(hits, h)
			last = max(last, h.Seq)
		}
	}
	if got.Epoch == epoch && last == seq {
		return
	}
	now := s.m.now()
	cfg := snap.torrent
	banned := false
	err = s.m.st.Tx(ctx, func(q *db.Queries) error {
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, h.Slot)
		}
		rows, err := q.SlotOwners(ctx, names)
		if err != nil {
			return err
		}
		owner := make(map[string]int64, len(rows))
		for _, r := range rows {
			owner[r.SlotName] = r.UserID
		}
		for _, h := range hits {
			uid, ok := owner[h.Slot]
			// A slot nobody owns any more, or a user let alone since the catch.
			if !ok || cfg.IsExempt(uid) {
				continue
			}
			var until int64
			if cfg.BanMinutes > 0 {
				until = now.Unix() + cfg.BanMinutes*60
				banned = true
			}
			if _, err := q.AddTorrentHit(ctx, db.AddTorrentHitParams{
				UserID: uid, NodeID: sql.NullInt64{Int64: s.id, Valid: true}, Ip: clip(h.IP, 64),
				Inbound: clip(h.Inbound, 64), Network: clip(h.Network, 8), Kind: clip(h.Kind, 16), Dest: clip(h.Dest, 300),
				Hits: int32(min(max(h.Count, 1), 1<<30)), At: now.Unix(), BannedUntil: until,
			}); err != nil {
				return err
			}
		}
		if err := q.SetNodeState(ctx, db.SetNodeStateParams{Key: stateKeyOf("torrent_epoch", s.id), Value: got.Epoch}); err != nil {
			return err
		}
		return q.SetNodeState(ctx, db.SetNodeStateParams{Key: stateKeyOf("torrent_seq", s.id), Value: strconv.FormatInt(last, 10)})
	})
	if err != nil {
		s.log.Error("store torrent hits", "err", err)
		return
	}
	if banned {
		s.m.PoliciesChanged()
	}
}

func (s *Syncer) torrentPos(ctx context.Context) (string, int64, error) {
	epoch, err := s.m.st.Q.GetNodeState(ctx, stateKeyOf("torrent_epoch", s.id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	raw, err := s.m.st.Q.GetNodeState(ctx, stateKeyOf("torrent_seq", s.id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	seq, _ := strconv.ParseInt(raw, 10, 64)
	return epoch, seq, nil
}

// clip cuts what a node sent to a sane length: a node is a server somebody else may run.
func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}
