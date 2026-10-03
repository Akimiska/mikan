package nodesync

import (
	"context"
	"crypto/x509"
	"database/sql"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// The background accounting writers (traffic batches of every node, the online marks and
// devices, period resets) touch the same user rows every few seconds. They lock the rows
// in id order and take turns on them: no retried conflicts or deadlocks, and every byte
// is counted once.
func TestBackgroundAccountingWritersDoNotConflict(t *testing.T) {
	s1, node1, st, users, now := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	var slots []string
	var ids []int64
	for i := range 5 {
		u, err := users.Create(ctx, domain.CreateInput{Name: fmt.Sprint("u", i), TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		slot, _ := st.Q.GetSlot(ctx, u.SlotID.Int64)
		slots = append(slots, slot.Name)
		ids = append(ids, u.ID)
	}
	panel, _ := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	n2, _, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: "B", Host: "198.51.100.20", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	node2 := &fakeNode{}
	s2 := s1.m.attach(t, n2.ID, Target{Node: node2, TLS: fakeTLS})

	const batches = 30
	const bytes = 1000
	before := st.Conflicts()
	feed := func(s *Syncer, node *fakeNode, epoch string) {
		for seq := int64(1); seq <= batches; seq++ {
			traffic := map[string]nodeapi.Traffic{}
			online := map[string]nodeapi.Online{}
			for _, slot := range slots {
				traffic[slot] = nodeapi.Traffic{Up: bytes, Down: bytes}
				online[slot] = nodeapi.Online{IPs: []string{"203.0.113.7"}, Conns: 1}
			}
			node.batch = nodeapi.Counters{Epoch: epoch, Seq: seq, Slots: traffic, Online: online}
			s.pullCounters(ctx)
		}
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); feed(s1, node1, "e1") }()
	go func() { defer wg.Done(); feed(s2, node2, "e2") }()
	go func() {
		defer wg.Done()
		for range batches {
			if err := s1.m.recordDevices(ctx, *now); err != nil {
				t.Error(err)
			}
			if err := s1.m.resetPeriods(ctx, *now); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()

	if got := st.Conflicts() - before; got != 0 {
		t.Errorf("%d serialization conflicts between background writers", got)
	}
	for _, id := range ids {
		u, err := st.Q.GetUser(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(2 * batches * bytes); u.UsedUp != want || u.UsedDown != want {
			t.Fatalf("user %d counted %d/%d, want %d each", id, u.UsedUp, u.UsedDown, want)
		}
		if !u.OnlineAt.Valid {
			t.Fatalf("user %d not marked online", id)
		}
	}
}

// Many users on two nodes at once, past their base quotas in the main traffic and in a
// pool: every byte counts once, and the grants pay exactly what went past each base, the
// soonest to expire first, whatever order the batches of the two nodes come in.
func TestConcurrentBatchesSpendGrantsExactly(t *testing.T) {
	s1, node1, st, users, now := setup(t)
	ctx := context.Background()
	q := st.Q
	pool, err := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	const (
		usersN    = 40
		batches   = 15
		mainBytes = 1000 // per user, node and batch
		poolBytes = 200
		limit     = 10_000 // the base: 2 nodes × 15 batches × 1000 = 30 000 go 20 000 past it
		poolLimit = 2_000  // 6 000 in the pool go 4 000 past it
		soon      = 15_000 // the main grant that ends first: used up
		later     = 10_000 // the endless one: 5 000 of it are left
		poolGrant = 5_000  // 1 000 left
	)
	tariffs, _ := q.ListTariffs(ctx)
	type acct struct {
		id                     int64
		slot                   string
		soon, later, poolGrant int64
	}
	var all []acct
	for i := range usersN {
		u, err := users.Create(ctx, domain.CreateInput{Name: fmt.Sprint("u", i), TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		l := int64(limit)
		if u, err = users.Update(ctx, u.ID, domain.Patch{TrafficLimit: &l}); err != nil {
			t.Fatal(err)
		}
		if err := q.SetUserPoolLimit(ctx, db.SetUserPoolLimitParams{UserID: u.ID, PoolID: pool.ID, TrafficLimit: sql.NullInt64{Int64: poolLimit, Valid: true}}); err != nil {
			t.Fatal(err)
		}
		grant := func(pool, bytes int64, lifetime string, days int64) int64 {
			g, err := domain.GrantTx(ctx, q, u, domain.GrantSpec{PoolID: pool, Bytes: bytes, Lifetime: lifetime, Days: days, Source: domain.SourceAdmin}, *now)
			if err != nil {
				t.Fatal(err)
			}
			return g.ID
		}
		slot, _ := q.GetSlot(ctx, u.SlotID.Int64)
		all = append(all, acct{id: u.ID, slot: slot.Name,
			later: grant(0, later, domain.LifetimeUsed, 0), soon: grant(0, soon, domain.LifetimeDays, 7), poolGrant: grant(pool.ID, poolGrant, domain.LifetimeUsed, 0)})
	}
	panel, _ := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	n2, _, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: "B", Host: "198.51.100.20", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	node2 := &fakeNode{}
	s2 := s1.m.attach(t, n2.ID, Target{Node: node2, TLS: fakeTLS})

	key := strconv.FormatInt(pool.ID, 10)
	before := st.Conflicts()
	feed := func(s *Syncer, node *fakeNode, epoch string) {
		for seq := int64(1); seq <= batches; seq++ {
			traffic := map[string]nodeapi.Traffic{}
			pools := map[string]map[string]nodeapi.Traffic{}
			for _, a := range all {
				traffic[a.slot] = nodeapi.Traffic{Up: mainBytes / 2, Down: mainBytes / 2}
				pools[a.slot] = map[string]nodeapi.Traffic{key: {Down: poolBytes}}
			}
			node.batch = nodeapi.Counters{Epoch: epoch, Seq: seq, Slots: traffic, Pools: pools}
			s.pullCounters(ctx)
			s.pullCounters(ctx) // delivered again: skipped
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); feed(s1, node1, "e1") }()
	go func() { defer wg.Done(); feed(s2, node2, "e2") }()
	wg.Wait()

	if got := st.Conflicts() - before; got != 0 {
		t.Errorf("%d conflicts between the batches", got)
	}
	left := map[int64]int64{}
	for _, a := range all {
		gs, err := q.ListUserGrants(ctx, a.id)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range gs {
			left[g.ID] = g.Remaining
		}
	}
	for _, a := range all {
		u, _ := q.GetUser(ctx, a.id)
		up, _ := q.GetUserPool(ctx, db.GetUserPoolParams{UserID: a.id, PoolID: pool.ID})
		if u.UsedUp+u.UsedDown != 2*batches*mainBytes || u.TotalDown != 2*batches*(mainBytes/2+poolBytes) || up.UsedDown != 2*batches*poolBytes {
			t.Fatalf("user %d: used %d/%d total down %d pool %d", a.id, u.UsedUp, u.UsedDown, u.TotalDown, up.UsedDown)
		}
		if left[a.soon] != 0 || left[a.later] != 5_000 || left[a.poolGrant] != 1_000 {
			t.Fatalf("user %d grants left: soonest %d, endless %d, pool %d", a.id, left[a.soon], left[a.later], left[a.poolGrant])
		}
	}
	day, _ := q.UserTrafficHourly(ctx, db.UserTrafficHourlyParams{UserID: all[0].id, Hour: 0})
	var sum int64
	for _, h := range day {
		sum += h.Up + h.Down
	}
	if sum != 2*batches*(mainBytes+poolBytes) {
		t.Fatalf("hourly statistics %d", sum)
	}
}
