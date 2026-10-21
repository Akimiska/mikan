package node

import (
	"math"
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > want*0.01 {
		t.Fatalf("%s: %.0f, want %.0f", what, got, want)
	}
}

func usage(g *group, bps, ceiling float64) used { return used{g: g, rate: bps, ceiling: ceiling} }

// Two users both pulling as much as they can split the channel in half.
func TestFairShareSplitsEvenly(t *testing.T) {
	a, b := newGroup("a"), newGroup("b")
	out := fairShares([]used{usage(a, 0, 0), usage(b, 0, 0)}, 100*mbit)
	near(t, "a", out[a], 50*mbit)
	near(t, "b", out[b], 50*mbit)
}

// A light user keeps an even share as its ceiling; what it leaves goes to the heavy one,
// so the channel is not wasted on a cap nobody uses.
func TestFairShareGivesWhatIsLeftToTheHungry(t *testing.T) {
	light, heavy := newGroup("light"), newGroup("heavy")
	out := fairShares([]used{usage(light, 10*mbit, 50*mbit), usage(heavy, 50*mbit, 50*mbit)}, 100*mbit)
	near(t, "light", out[light], 50*mbit)
	near(t, "heavy", out[heavy], 90*mbit)
}

// A user held by an own cap below the even share takes no more of the channel than that.
func TestFairShareRespectsOwnCap(t *testing.T) {
	capped, free := newGroup("capped"), newGroup("free")
	capped.own.Store(20 * mbit)
	out := fairShares([]used{usage(capped, 20*mbit, 20*mbit), usage(free, 50*mbit, 50*mbit)}, 100*mbit)
	near(t, "free", out[free], 80*mbit)
	if lower(float64(capped.own.Load()), out[capped]) != 20*mbit {
		t.Fatalf("the own cap must stay the ceiling: %.0f", lower(float64(capped.own.Load()), out[capped]))
	}
}

// One user alone gets the whole channel, and a share never sinks under the floor.
func TestFairShareAloneAndFloor(t *testing.T) {
	a := newGroup("a")
	out := fairShares([]used{usage(a, 0, 0)}, 100*mbit)
	near(t, "alone", out[a], 100*mbit)

	var many []used
	for range 500 {
		many = append(many, usage(newGroup("g"), 0, 0))
	}
	for _, s := range fairShares(many, 10*mbit) {
		if s < shareFloor {
			t.Fatalf("share %.0f under the floor", s)
		}
	}
}

func cappedRegistry(t *testing.T, now *time.Time) (*Registry, *fakeCore) {
	t.Helper()
	core := &fakeCore{}
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return *now }, core)
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}, {Name: "s2", UUID: "u2"}, {Name: "s3", UUID: "u3"}})
	r.SetInbounds(map[string]bool{"vless": false, "tuic": true})
	return r, core
}

// The policy's cap holds the user's devices as one group: the core gets the cap as the
// speed of every slot on every inbound, with one speed group for the user, so all its
// devices and inbounds share it. A user without a cap gets no limit at all.
func TestPolicyCapGoesToTheCore(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r, core := cappedRegistry(t, &now)
	r.SetPolicies("e1", []nodeapi.Policy{
		{Slot: "s1", Allowed: true, QuotaRemaining: -1, SpeedMbps: 50, Group: "7"},
		{Slot: "s2", Allowed: true, QuotaRemaining: -1, SpeedMbps: 50, Group: "7"},
		{Slot: "s3", Allowed: true, QuotaRemaining: -1, Group: "8"},
	})
	capped := fakeLimit{group: "mikan/7", up: 50 * mbit, down: 50 * mbit}
	for _, k := range []userKey{{"vless", "s1"}, {"tuic", "u1"}, {"vless", "s2"}, {"tuic", "u2"}} {
		if got := core.held[k]; got != capped {
			t.Fatalf("%v: %+v, want %+v", k, got, capped)
		}
	}
	if _, ok := core.held[userKey{"vless", "s3"}]; ok {
		t.Fatalf("a user without a cap must have no limit: %v", core.held)
	}

	// A lifted cap lifts the limit.
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1, Group: "7"}})
	for _, k := range []userKey{{"vless", "s1"}, {"tuic", "u1"}} {
		if l, ok := core.held[k]; ok {
			t.Fatalf("no cap, no limit: %v %+v", k, l)
		}
	}
}

// A cap and a quota go to the core as one limit: SetLimit there replaces a whole limit.
func TestCapAndBudgetTogether(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r, core := cappedRegistry(t, &now)
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: 1000, SpeedMbps: 10, Group: "7"}})
	want := fakeLimit{budget: 1000, group: "mikan/7", up: 10 * mbit, down: 10 * mbit}
	if got := core.held[userKey{"vless", "s1"}]; got != want {
		t.Fatalf("%+v, want %+v", got, want)
	}
	// The quota taken off (unlimited traffic now) with the speed kept: the core drops it.
	passes := core.passes
	r.SetPolicies("e1", []nodeapi.Policy{{Slot: "s1", Allowed: true, QuotaRemaining: -1, SpeedMbps: 10, Group: "7"}})
	if got := core.held[userKey{"vless", "s1"}]; got.budget != 0 || got.down != 10*mbit || core.passes == passes {
		t.Fatalf("the quota taken off stays in the core: %+v", got)
	}
}

// With the fair share on, the active users' shares go to the core as their speeds, and a
// share that moves by a hair is left as the core holds it.
func TestFairShareGoesToTheCore(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r, core := cappedRegistry(t, &now)
	r.SetPolicies("e1", []nodeapi.Policy{
		{Slot: "s1", Allowed: true, QuotaRemaining: -1, Group: "7"},
		{Slot: "s2", Allowed: true, QuotaRemaining: -1, Group: "8"},
	})
	r.SetShaping(&nodeapi.Shaping{ChannelMbps: 100})
	// Both move traffic: each gets half the channel.
	s1, s2 := r.lookup("s1"), r.lookup("s2")
	s1.countIn("", 10*mbit, 10*mbit)
	s2.countIn("", 10*mbit, 10*mbit)
	r.shaper.share()
	r.reconcile()
	if got := core.held[userKey{"vless", "s1"}]; got.group != "mikan/7" || got.down != 50*mbit {
		t.Fatalf("s1: %+v", got)
	}
	if got := core.held[userKey{"tuic", "u2"}]; got.group != "mikan/8" || got.down != 50*mbit {
		t.Fatalf("s2: %+v", got)
	}
	passes := core.passes
	// The same again: nothing to tell the core.
	s1.countIn("", 10*mbit, 10*mbit)
	s2.countIn("", 10*mbit, 10*mbit)
	r.shaper.share()
	r.reconcile()
	if core.passes != passes {
		t.Fatalf("an unchanged share went to the core again")
	}
	// Off: the limits go.
	r.SetShaping(nil)
	for _, k := range []userKey{{"vless", "s1"}, {"tuic", "u1"}, {"vless", "s2"}, {"tuic", "u2"}} {
		if l, ok := core.held[k]; ok {
			t.Fatalf("no fair share, no cap, no limit: %v %+v", k, l)
		}
	}
}

func TestSameSpeed(t *testing.T) {
	for _, c := range []struct {
		held, want int64
		same       bool
	}{
		{0, 0, true},
		{100, 101, true},
		{100, 103, false},
		{0, 100, false},
		{100, 0, false},
	} {
		if got := sameSpeed(c.held, c.want); got != c.same {
			t.Fatalf("sameSpeed(%d, %d) = %v", c.held, c.want, got)
		}
	}
}
