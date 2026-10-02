package nodesync

import (
	"context"
	"crypto/x509"
	"fmt"
	"sync"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
	"mikan/internal/panel/domain"
)

// The background accounting writers (traffic batches of every node, the online marks and
// devices, period resets) touch the same user rows every few seconds. One at a time they
// never abort each other: no serializable retries, and every byte is counted once.
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
