package node

import (
	"reflect"
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

// Activity is what the panel's block detector reads: per device, the last time each
// inbound let it in. Refused connections do not count and old entries drop out.
func TestActivityPerDeviceAndInbound(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return now }, &fakeCore{})
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}, {Name: "s2", UUID: "u2"}})
	r.SetPolicies("e1", []nodeapi.Policy{
		{Slot: "s1", Allowed: true, QuotaRemaining: -1},
		{Slot: "s2", Allowed: true, QuotaRemaining: -1, Inbounds: []string{"tuic"}},
	})
	r.admitIn("u1", "vless-xhttp", "203.0.113.1")
	now = now.Add(10 * time.Second)
	r.admitIn("s1", "hysteria2", "203.0.113.1")
	r.admitIn("u1", "tuic", "203.0.113.2")
	if r.admitIn("u2", "vless-xhttp", "198.51.100.7") != nil {
		t.Fatal("s2 is limited to tuic")
	}
	r.admitIn("u2", "tuic", "198.51.100.7")

	want := []nodeapi.ClientActivity{
		{Slot: "s1", IP: "203.0.113.1", Seen: map[string]int64{"vless-xhttp": now.Unix() - 10, "hysteria2": now.Unix()}},
		{Slot: "s1", IP: "203.0.113.2", Seen: map[string]int64{"tuic": now.Unix()}},
		{Slot: "s2", IP: "198.51.100.7", Seen: map[string]int64{"tuic": now.Unix()}},
	}
	if got := r.Activity().Clients; !reflect.DeepEqual(got, want) {
		t.Fatalf("activity:\n got %+v\nwant %+v", got, want)
	}

	now = now.Add(activityKeep + time.Second)
	r.admitIn("u1", "tuic", "203.0.113.2")
	got := r.Activity().Clients
	if len(got) != 1 || got[0].IP != "203.0.113.2" || len(got[0].Seen) != 1 {
		t.Fatalf("entries older than %s must drop out: %+v", activityKeep, got)
	}
}

// A device limit is shared by all nodes of a panel: devices the panel saw on other nodes
// take places here, and those same devices may still connect here.
func TestDeviceLimitCountsOtherNodes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return now }, &fakeCore{})
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	policy := nodeapi.Policy{Slot: "s1", Allowed: true, DeviceLimit: 2, QuotaRemaining: -1, OtherIPs: []string{"198.51.100.1"}}
	r.SetPolicies("e1", []nodeapi.Policy{policy})

	if r.admitIn("u1", "vless", "203.0.113.1") == nil {
		t.Fatal("the second device must connect")
	}
	if r.admitIn("u1", "vless", "203.0.113.2") != nil {
		t.Fatal("a third device must be refused: one is already online on another node")
	}
	if r.admitIn("u1", "vless", "198.51.100.1") == nil {
		t.Fatal("the device from the other node must connect here without taking a place")
	}

	// Without devices elsewhere the limit is this node's alone.
	policy.OtherIPs = nil
	r.SetPolicies("e1", []nodeapi.Policy{policy})
	now = now.Add(2 * time.Minute) // both local devices are released
	for _, ip := range []string{"203.0.113.3", "203.0.113.4"} {
		if r.admitIn("u1", "vless", ip) == nil {
			t.Fatalf("%s must connect", ip)
		}
	}
	if r.admitIn("u1", "vless", "203.0.113.5") != nil {
		t.Fatal("the limit still holds on one node")
	}
}

// Listeners with one key for everyone (Shadowsocks-2022, Sudoku, Snell) carry no user:
// the node lets them in without a policy or per-user limits — only on those listeners.
func TestSharedListeners(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return now }, &fakeCore{})
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	r.SetShared([]string{"ss"})
	if r.admitIn("", "ss", "203.0.113.1") == nil {
		t.Fatal("a shared listener takes connections without a user")
	}
	if r.admitIn("", "vless-xhttp", "203.0.113.1") != nil {
		t.Fatal("every other listener still needs a user")
	}
	if got := r.Activity().Clients; len(got) != 0 {
		t.Fatalf("shared traffic is nobody's activity: %+v", got)
	}
	r.SetShared(nil)
	if r.admitIn("", "ss", "203.0.113.1") != nil {
		t.Fatal("the shared listener is gone")
	}
}

// The core counts each user of each inbound: the registry adds a slot's traffic up across
// its inbounds, into the main quota or the inbound's traffic pool, and leaves the relay's
// users (other nodes, counted apart: TestRelayCountsApart) and unknown users out of the
// slots. TUIC knows its users by UUID.
func TestDrainAddsUpTheCoresCounters(t *testing.T) {
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, time.Now, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "AAAA-1"}})
	r.SetInbounds(map[string]bool{"vless": false, "tuic": true, "wl": false})
	r.SetPools(map[string]string{"wl": "7"})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	core.moved("vless", "s1", 100, 1000)
	core.moved("tuic", "aaaa-1", 10, 20)
	core.moved("wl", "s1", 5, 5)
	core.moved(nodeapi.RelayListener, "s1", 1<<30, 1<<30)
	core.moved("vless", "stranger", 1<<30, 1<<30)
	c := r.Counters()
	if c.Slots["s1"] != (nodeapi.Traffic{Up: 110, Down: 1020}) || c.Pools["s1"]["7"] != (nodeapi.Traffic{Up: 5, Down: 5}) || len(c.Slots) != 1 {
		t.Fatalf("batch: slots %v pools %v", c.Slots, c.Pools)
	}

	// A slot removed meanwhile still gets the traffic it moved before it went.
	r.Ack(c.Epoch, c.Seq)
	core.moved("vless", "s1", 7, 7)
	r.SetSlots(nil)
	if c := r.Counters(); c.Slots["s1"] != (nodeapi.Traffic{Up: 7, Down: 7}) {
		t.Fatalf("the removed slot's last traffic: %v", c.Slots)
	}
}

// The relay's traffic (the other nodes' cascades) goes in the batch apart, by source, and
// none of it to a slot, not even to one of the same name; its open connections are a live
// view of their own. An outstanding batch neither loses nor repeats it, relay bytes alone
// make a batch, and what no batch took yet survives a restart.
func TestRelayCountsApart(t *testing.T) {
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, time.Now, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "AAAA-1"}})
	r.SetInbounds(map[string]bool{"vless": false})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	core.moved("vless", "s1", 10, 100)
	core.moved(nodeapi.RelayListener, "relay-2", 300, 3000)
	core.moved(nodeapi.RelayListener, "s1", 5, 50)
	core.online(nodeapi.RelayListener, "relay-2", "198.51.100.20", 3, time.Now())
	c := r.Counters()
	if c.Slots["s1"] != (nodeapi.Traffic{Up: 10, Down: 100}) || len(c.Slots) != 1 {
		t.Fatalf("slots: %v", c.Slots)
	}
	if c.Relay["relay-2"] != (nodeapi.Traffic{Up: 300, Down: 3000}) || c.Relay["s1"] != (nodeapi.Traffic{Up: 5, Down: 50}) || len(c.Relay) != 2 {
		t.Fatalf("relay: %v", c.Relay)
	}
	if c.RelayOnline["relay-2"] != 3 || len(c.RelayOnline) != 1 {
		t.Fatalf("relay online: %v", c.RelayOnline)
	}
	if _, ok := c.Online["relay-2"]; ok {
		t.Fatal("the relay among the users online")
	}

	core.moved(nodeapi.RelayListener, "relay-2", 1, 1)
	if again := r.Counters(); again.Seq != c.Seq || again.Relay["relay-2"] != c.Relay["relay-2"] {
		t.Fatalf("the outstanding batch: %+v", again)
	}
	r.Ack(c.Epoch, c.Seq)
	next := r.Counters()
	if next.Idle || next.Relay["relay-2"] != (nodeapi.Traffic{Up: 1, Down: 1}) || len(next.Slots) != 0 {
		t.Fatalf("relay bytes alone: %+v", next)
	}
	r.Ack(next.Epoch, next.Seq)

	core.moved(nodeapi.RelayListener, "relay-2", 9, 9)
	r.drain()
	after := NewRegistry("e1", 0, time.Minute, time.Now, &fakeCore{})
	after.restore(r.snapshot())
	if b := after.Counters(); b.Relay["relay-2"] != (nodeapi.Traffic{Up: 9, Down: 9}) {
		t.Fatalf("after a restart: %+v", b)
	}
}

// Whom the core keeps out follows the policy: a slot not allowed is out of every inbound,
// one limited to some inbounds out of the rest, and a quota run out keeps the slot out of
// the inbounds that count to it, until the panel sends a new one.
func TestCoreKeepsOutWhomThePolicyDenies(t *testing.T) {
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, time.Now, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}, {Name: "s2", UUID: "u2"}, {Name: "s3", UUID: "u3"}})
	r.SetInbounds(map[string]bool{"vless": false, "tuic": true, "wl": false})
	r.SetPools(map[string]string{"wl": "7"})
	r.SetPolicies("e1", []nodeapi.Policy{
		{Slot: "s1", Allowed: true, QuotaRemaining: 1000, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 100}}},
		{Slot: "s2", Allowed: true, QuotaRemaining: -1, Inbounds: []string{"tuic"}},
		// s3 has no policy: denied.
	})
	want := []string{"tuic/u3", "vless/s2", "vless/s3", "wl/s2", "wl/s3"}
	if got := core.keptOut(); !reflect.DeepEqual(got, want) {
		t.Fatalf("kept out %v, want %v", got, want)
	}

	core.moved("wl", "s1", 60, 60) // the pool runs out, the main quota does not
	r.Counters()
	if got := core.keptOut(); !reflect.DeepEqual(got, append([]string{"tuic/u3", "vless/s2", "vless/s3", "wl/s1"}, "wl/s2", "wl/s3")) {
		t.Fatalf("an exhausted pool keeps the slot out of its inbound only: %v", got)
	}
	if r.admitIn("s1", "wl", "203.0.113.1") != nil || r.admitIn("s1", "vless", "203.0.113.1") == nil {
		t.Fatal("the node's own admission agrees with the core")
	}
	core.moved("vless", "s1", 1000, 0)
	c := r.Counters()
	if got := core.keptOut(); !reflect.DeepEqual(got, []string{"tuic/u1", "tuic/u3", "vless/s1", "vless/s2", "vless/s3", "wl/s1", "wl/s2", "wl/s3"}) {
		t.Fatalf("an exhausted main quota keeps the slot out of the rest: %v", got)
	}
	r.Ack(c.Epoch, c.Seq)
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: 5000, BaseSeq: c.Seq}})
	if got := core.keptOut(); !reflect.DeepEqual(got, []string{"tuic/u2", "tuic/u3", "vless/s2", "vless/s3", "wl/s2", "wl/s3"}) {
		t.Fatalf("a new quota and no pool limit let s1 back in: %v", got)
	}
}

// A device holds its place while the core lists it as connected, and for the release
// time after it left; one only just let in holds a short place until the core lists it.
func TestDeviceHoldsItsPlaceWhileTheCoreListsIt(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	core := &fakeCore{}
	r := NewRegistry("e1", 0, 10*time.Second, func() time.Time { return now }, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetInbounds(map[string]bool{"hy2": false})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1, DeviceLimit: 1}})
	if r.admitIn("u1", "hy2", "203.0.113.1") == nil {
		t.Fatal("the first device")
	}
	if r.admitIn("u1", "hy2", "203.0.113.2") != nil {
		t.Fatal("a second device right after the first")
	}
	core.online("hy2", "s1", "203.0.113.1", 2, now)
	now = now.Add(time.Minute)
	r.drain()
	if r.admitIn("u1", "hy2", "203.0.113.2") != nil {
		t.Fatal("the first device is still connected")
	}
	if got := r.Counters().Online["s1"]; got.Conns != 2 || !reflect.DeepEqual(got.IPs, []string{"203.0.113.1"}) {
		t.Fatalf("online: %+v", got)
	}
	core.online("hy2", "s1", "203.0.113.1", 0, now)
	now = now.Add(5 * time.Second)
	r.drain()
	if r.admitIn("u1", "hy2", "203.0.113.2") != nil {
		t.Fatal("the device left 5 s ago: its place holds for 10")
	}
	now = now.Add(6 * time.Second)
	r.drain()
	if r.admitIn("u1", "hy2", "203.0.113.2") == nil {
		t.Fatal("the release time is over: the place is free")
	}
}

// The core cuts a user of one inbound at the quota left by itself, to the byte: each
// inbound gets the quota it counts to as a budget, counted anew only when the panel sends
// a quota, never re-set by the registry's own drains.
func TestQuotaBudgetsInTheCore(t *testing.T) {
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, time.Now, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	r.SetInbounds(map[string]bool{"vless": false, "tuic": true, "wl": false})
	r.SetPools(map[string]string{"wl": "7"})
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: 1000, Pools: []nodeapi.PoolQuota{{Pool: "7", Remaining: 100}}}})
	want := map[userKey]fakeLimit{{"vless", "s1"}: {budget: 1000}, {"tuic", "u1"}: {budget: 1000}, {"wl", "s1"}: {budget: 100}}
	if !reflect.DeepEqual(core.held, want) || core.fresh != 3 {
		t.Fatalf("budgets %v (fresh %d), want %v", core.held, core.fresh, want)
	}
	core.moved("vless", "s1", 300, 0)
	r.Counters()
	r.Counters()
	if !reflect.DeepEqual(core.held, want) || core.fresh != 3 {
		t.Fatalf("a drain re-set the budgets: %v (fresh %d)", core.held, core.fresh)
	}
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: 700, BaseSeq: 1}})
	want = map[userKey]fakeLimit{{"vless", "s1"}: {budget: 700}, {"tuic", "u1"}: {budget: 700}}
	if !reflect.DeepEqual(core.held, want) || core.fresh != 5 {
		t.Fatalf("a new quota is a fresh budget, a pool without one has none: %v (fresh %d)", core.held, core.fresh)
	}
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1}})
	if len(core.held) != 0 {
		t.Fatalf("no quota, no limit: %v", core.held)
	}
}
