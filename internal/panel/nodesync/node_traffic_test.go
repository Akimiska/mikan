package nodesync

import (
	"context"
	"crypto/x509"
	"strconv"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// What each node carried is counted with the users' statistics, on the same transaction
// and from the same bytes: the nodes' hours, days and running totals add up to the users',
// a batch delivered twice counts once, and what the users' statistics leave out (a pool
// that is gone, a slot nobody owns) is left out of the nodes' too.
func TestNodeTrafficAddsUpToTheUsers(t *testing.T) {
	s1, node1, st, users, now := setup(t)
	ctx := context.Background()
	q := st.Q
	tariffs, _ := q.ListTariffs(ctx)
	pool, err := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "WL", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	key := strconv.FormatInt(pool.ID, 10)
	var slots []string
	for _, name := range []string{"a", "b"} {
		u, err := users.Create(ctx, domain.CreateInput{Name: name, TariffID: tariffs[1].ID})
		if err != nil {
			t.Fatal(err)
		}
		slot, _ := q.GetSlot(ctx, u.SlotID.Int64)
		slots = append(slots, slot.Name)
	}
	panel, _ := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, time.Now())
	n2, _, err := domain.AddNode(ctx, st, panel, domain.NodeInput{Name: "B", Host: "198.51.100.20", APIPort: 40000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	node2 := &fakeNode{}
	s2 := s1.m.attach(t, n2.ID, Target{Node: node2, TLS: fakeTLS})

	// The first node carries main traffic and pool traffic, the second only main; both
	// report a slot nobody owns and a pool that does not exist.
	node1.batch = nodeapi.Counters{Epoch: "e1", Seq: 1,
		Slots: map[string]nodeapi.Traffic{slots[0]: {Up: 10, Down: 100}, slots[1]: {Up: 1, Down: 2}, "ghost": {Up: 7, Down: 7}},
		Pools: map[string]map[string]nodeapi.Traffic{slots[0]: {key: {Up: 5, Down: 50}, "999": {Down: 9}}}}
	s1.pullCounters(ctx)
	s1.pullCounters(ctx) // the same batch again: counted once
	node2.batch = nodeapi.Counters{Epoch: "e2", Seq: 1, Slots: map[string]nodeapi.Traffic{slots[1]: {Up: 300, Down: 3000}}}
	s2.pullCounters(ctx)
	// Later in the same day, and in the next hour.
	*now = now.Add(time.Hour)
	node1.batch = nodeapi.Counters{Epoch: "e1", Seq: 2, Slots: map[string]nodeapi.Traffic{slots[0]: {Up: 20, Down: 200}}}
	s1.pullCounters(ctx)

	sum := func(table string, extra string) (up, down int64) {
		t.Helper()
		if err := st.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM "+table+extra).Scan(&up, &down); err != nil {
			t.Fatal(err)
		}
		return up, down
	}
	// Node 1: (10+5) + (1) + 20 up, (100+50) + 2 + 200 down; node 2: 300 and 3000.
	if up, down := sum("node_traffic_hourly", " WHERE node_id = 1"); up != 36 || down != 352 {
		t.Fatalf("node 1 by the hour: %d/%d, want 36/352", up, down)
	}
	if up, down := sum("node_traffic_hourly", " WHERE node_id = "+strconv.FormatInt(n2.ID, 10)); up != 300 || down != 3000 {
		t.Fatalf("node 2 by the hour: %d/%d, want 300/3000", up, down)
	}
	var hours int
	if err := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM node_traffic_hourly WHERE node_id = 1").Scan(&hours); err != nil || hours != 2 {
		t.Fatalf("node 1 has %d hourly rows, want 2: %v", hours, err)
	}
	// The nodes together are the users together, by the hour, by the day and in the totals.
	uh, dh := sum("traffic_hourly", "")
	nh, nd := sum("node_traffic_hourly", "")
	if uh != nh || dh != nd || uh != 336 || dh != 3352 {
		t.Fatalf("hourly: users %d/%d, nodes %d/%d, want 336/3352", uh, dh, nh, nd)
	}
	uu, ud := sum("traffic_daily", "")
	du, dd := sum("node_traffic_daily", "")
	if uu != du || ud != dd || uu != uh || ud != dh {
		t.Fatalf("daily: users %d/%d, nodes %d/%d", uu, ud, du, dd)
	}
	var totalUp, totalDown int64
	if err := st.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(total_up),0), COALESCE(SUM(total_down),0) FROM nodes").Scan(&totalUp, &totalDown); err != nil || totalUp != uh || totalDown != dh {
		t.Fatalf("running totals %d/%d, want %d/%d: %v", totalUp, totalDown, uh, dh, err)
	}
	n1, _ := q.GetNode(ctx, 1)
	if n1.TotalUp != 36 || n1.TotalDown != 352 {
		t.Fatalf("node 1's total %d/%d", n1.TotalUp, n1.TotalDown)
	}
}

// A node that is deleted while its batch is being counted does not fail the batch: the
// users' traffic still counts, and the node's rows go with the node.
func TestNodeTrafficSkipsANodeThatIsGone(t *testing.T) {
	_, _, st, users, now := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, err := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(ctx, func(q *db.Queries) error {
		return domain.CountTraffic(ctx, q, domain.TrafficBatch{Main: map[int64]domain.Bytes{u.ID: {Up: 4, Down: 6}}, Node: 4242}, *now)
	})
	if err != nil {
		t.Fatalf("a batch of a node that is gone: %v", err)
	}
	got, _ := st.Q.GetUser(ctx, u.ID)
	if got.UsedUp != 4 || got.UsedDown != 6 {
		t.Fatalf("the user's traffic: %d/%d", got.UsedUp, got.UsedDown)
	}
	var rows int
	if err := st.DB.QueryRowContext(ctx, "SELECT count(*) FROM node_traffic_hourly").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows of a node that is gone: %d %v", rows, err)
	}
}
