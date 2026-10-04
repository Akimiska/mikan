package nodesync

import (
	"context"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/torrent"
)

type torrentNode struct {
	*fakeNode
	hits  nodeapi.TorrentHits
	asked int
}

func (n *torrentNode) Torrents(_ context.Context, epoch string, after int64) (nodeapi.TorrentHits, error) {
	n.asked++
	out := nodeapi.TorrentHits{Epoch: n.hits.Epoch}
	for _, h := range n.hits.Hits {
		if epoch != n.hits.Epoch || h.Seq > after {
			out.Hits = append(out.Hits, h)
		}
	}
	return out, nil
}

func policyOf(t *testing.T, s *Syncer, userID int64) nodeapi.Policy {
	t.Helper()
	_, ps, owners, err := s.policies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if owners[p.Slot] == userID {
			return p
		}
	}
	t.Fatalf("no policy for user %d", userID)
	return nodeapi.Policy{}
}

// A catch a node reports bans its user on every node from the panel's clock, once, and
// the exempt are left alone.
func TestTorrentHitsBanOnEveryNode(t *testing.T) {
	s, fake, st, users, now := setup(t)
	ctx := context.Background()
	node := &torrentNode{fakeNode: fake}
	s.node = node
	tariffs, _ := st.Q.ListTariffs(ctx)
	caught, _ := users.Create(ctx, domain.CreateInput{Name: "caught", TariffID: tariffs[2].ID})
	free, _ := users.Create(ctx, domain.CreateInput{Name: "free", TariffID: tariffs[2].ID})
	slotOf := func(u db.User) string {
		sl, err := st.Q.GetSlot(ctx, u.SlotID.Int64)
		if err != nil {
			t.Fatal(err)
		}
		return sl.Name
	}
	node.hits = nodeapi.TorrentHits{Epoch: "e1", Hits: []nodeapi.TorrentHit{
		{Seq: 1, Slot: slotOf(caught), IP: "203.0.113.5", Inbound: "vless", Network: "tcp", Kind: nodeapi.TorrentHandshake, Dest: "198.51.100.1:6881", Count: 3, BannedUntil: 1},
		{Seq: 2, Slot: slotOf(free), IP: "203.0.113.6", Inbound: "vless", Network: "udp", Kind: nodeapi.TorrentDHT, Dest: "198.51.100.2:6881", Count: 1},
		{Seq: 3, Slot: "s-nobody", IP: "203.0.113.7", Network: "udp", Kind: nodeapi.TorrentUTP, Count: 1},
	}}

	s.pullTorrents(ctx)
	if node.asked != 0 {
		t.Fatal("a blocker that is off does not ask the nodes")
	}

	cfg := torrent.Config{Enabled: true, BanMinutes: 30, Exempt: []int64{free.ID}}
	if err := settings.Set(ctx, settings.New(st.Q), torrent.KeyConfig, cfg); err != nil {
		t.Fatal(err)
	}
	s.m.SlotsChanged()
	st0, err := s.desired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st0.Torrent == nil || st0.Torrent.BanSeconds != 1800 {
		t.Fatalf("the nodes get the blocker with their state: %+v", st0.Torrent)
	}
	if p := policyOf(t, s, free.ID); !p.TorrentExempt {
		t.Fatalf("the exempt user's policy says so: %+v", p)
	}

	s.pullTorrents(ctx)
	s.pullTorrents(ctx) // the same hits again: the cursor skips them
	rows, err := st.Q.ListTorrentHits(ctx, db.ListTorrentHitsParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UserID != caught.ID || rows[0].Hits != 3 || rows[0].BannedUntil != now.Unix()+1800 {
		t.Fatalf("one hit, the exempt and the unknown left out, the ban from the panel's clock: %+v", rows)
	}
	if p := policyOf(t, s, caught.ID); p.BannedUntil != now.Unix()+1800 {
		t.Fatalf("the ban goes to the nodes: %+v", p)
	}

	// The node restarted: its sequence starts over under a new epoch.
	node.hits = nodeapi.TorrentHits{Epoch: "e2", Hits: []nodeapi.TorrentHit{
		{Seq: 1, Slot: slotOf(caught), IP: "203.0.113.5", Network: "udp", Kind: nodeapi.TorrentUTP, Count: 1},
	}}
	*now = now.Add(time.Minute)
	s.pullTorrents(ctx)
	if rows, _ = st.Q.ListTorrentHits(ctx, db.ListTorrentHitsParams{Limit: 10}); len(rows) != 2 {
		t.Fatalf("a new epoch's hits are stored: %d", len(rows))
	}
	s.m.PoliciesChanged()
	if p := policyOf(t, s, caught.ID); p.BannedUntil != now.Unix()+1800 {
		t.Fatalf("the latest ban holds: %d, want %d", p.BannedUntil, now.Unix()+1800)
	}

	if n, err := st.Q.LiftTorrentBans(ctx, db.LiftTorrentBansParams{UserID: caught.ID, Now: now.Unix()}); err != nil || n != 2 {
		t.Fatalf("lift: %d %v", n, err)
	}
	s.m.PoliciesChanged()
	if p := policyOf(t, s, caught.ID); p.BannedUntil != 0 {
		t.Fatalf("a lifted ban is gone: %+v", p)
	}

	// Switched off, the bans stop holding: the admin turned the blocker off, not just the catching.
	cfg.Enabled = false
	_ = settings.Set(ctx, settings.New(st.Q), torrent.KeyConfig, cfg)
	s.m.SlotsChanged()
	if st1, _ := s.desired(ctx); st1.Torrent != nil {
		t.Fatal("the blocker is off on the nodes")
	}
}
