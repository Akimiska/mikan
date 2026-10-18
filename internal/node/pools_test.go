package node

import (
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

// A traffic pool has its own counter and quota: running it out keeps the slot out of its
// own inbounds only, the main quota does the same the other way round, and the batch
// reports both apart.
func TestTrafficPools(t *testing.T) {
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, time.Now, core)
	r.SetSlots([]nodeapi.Slot{{Name: "u1", UUID: "uuid-1"}})
	r.SetInbounds(map[string]bool{"in-wl": false, "in-de": false})
	r.SetPools(map[string]string{"in-wl": "7"})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: 1000, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 500}}}})

	core.moved("in-wl", "u1", 300, 300) // 600 > 500: the pool runs out
	r.drain()
	if r.admitIn("u1", "in-wl", "203.0.113.1") != nil {
		t.Fatal("an exhausted pool still lets its inbound in")
	}
	if r.admitIn("u1", "in-de", "203.0.113.1") == nil {
		t.Fatal("the pool running out cut the main quota's inbounds too")
	}
	s := r.lookup("u1")
	if s.remaining.Load() != 1000 {
		t.Fatalf("main quota left %d, want 1000: the pool's bytes count to the pool", s.remaining.Load())
	}
	core.moved("in-de", "u1", 1000, 0)
	r.drain()
	if r.admitIn("u1", "in-de", "203.0.113.1") != nil {
		t.Fatal("an exhausted main quota still lets its inbounds in")
	}

	c := r.Counters()
	if c.Slots["u1"] != (nodeapi.Traffic{Up: 1000}) || c.Pools["u1"]["7"] != (nodeapi.Traffic{Up: 300, Down: 300}) {
		t.Fatalf("batch: slots %v pools %v", c.Slots, c.Pools)
	}
	if !r.Ack(c.Epoch, c.Seq) {
		t.Fatal("ack")
	}
	// A new period: the panel sends fresh quotas, both open again.
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: 1000, BaseSeq: c.Seq, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 500}}}})
	if r.admitIn("u1", "in-wl", "203.0.113.1") == nil {
		t.Fatal("a renewed pool stays closed")
	}
	// A pool the policy does not name has no limit.
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "u1", Allowed: true, QuotaRemaining: -1, BaseSeq: c.Seq}})
	core.moved("in-wl", "u1", 1<<40, 0)
	r.drain()
	if r.admitIn("u1", "in-wl", "203.0.113.1") == nil {
		t.Fatal("a pool without a quota was cut")
	}

	// The counters survive a restart, pools included.
	st := r.snapshot()
	r2 := NewRegistry("e1", 0, time.Minute, time.Now, &fakeCore{})
	r2.SetSlots([]nodeapi.Slot{{Name: "u1", UUID: "uuid-1"}})
	r2.restore(st)
	if c := r2.Counters(); c.Pools["u1"]["7"].Up != 1<<40 {
		t.Fatalf("pool counters after restart: %v", c.Pools)
	}
}
