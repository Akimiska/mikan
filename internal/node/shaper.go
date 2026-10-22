package node

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/nodeapi"
)

// Speed caps and the fair share of a node's channel.
//
// A user's slots (one per bound device) form a group with one ceiling each way, so the cap
// is the user's on this node, whatever the devices, inbounds and connections. The core
// holds the traffic to it: every slot of the group gets the group's ceiling as its speed on
// every inbound, with the group as its speed group (Citrus speed-group), so they all take
// from one pair of buckets, TCP and UDP alike (reconcile). The node only works the ceilings
// out; the bytes a group moved come from the core's counters (Registry.drain).
//
// The fair share splits the channel max-min every second: a group that moved less than an
// even share keeps an even share as its ceiling, what it leaves goes to the groups that
// hit their ceilings. Idle groups count for nothing, so one user alone gets the whole
// channel.

const (
	mbit = 1_000_000 / 8 // bytes a second in one Mbit/s

	// shareEvery is how often the fair share is worked out again.
	shareEvery = time.Second
	// activeFor is how long a group counts as active after it last moved traffic.
	activeFor = 3 * time.Second
	// shareFloor keeps a share from sinking to nothing among very many users.
	shareFloor = 1 * mbit
	// hungryAt: a group that used this much of its ceiling wants more.
	hungryAt = 0.85
)

// group is a user's traffic on this node: its cap and its ceilings each way.
type group struct {
	key        string       // the policy's group, or the slot's name
	own        atomic.Int64 // its own cap, bytes/s each way; 0: none
	up, down   atomic.Int64 // the ceilings the core holds it to, bytes/s; 0: none
	upN, downN atomic.Int64 // bytes since the last share
	lastActive atomic.Int64 // unix nanoseconds
}

func newGroup(key string) *group { return &group{key: key} }

// speedGroup is the name the core shares the group's buckets under.
func (g *group) speedGroup() string { return "mikan/" + g.key }

// moved counts bytes the group moved; share turns them into its rate and its activity.
func (g *group) moved(up, down int64) {
	if up != 0 {
		g.upN.Add(up)
	}
	if down != 0 {
		g.downN.Add(down)
	}
}

// shaper keeps the groups and the fair share of the node.
type shaper struct {
	mu      sync.Mutex
	groups  map[string]*group
	channel atomic.Int64 // bytes/s each way; 0: no fair share
	now     func() time.Time
}

func newShaper(now func() time.Time) *shaper {
	return &shaper{groups: map[string]*group{}, now: now}
}

// group is the group of key, made on first use.
func (sh *shaper) group(key string) *group {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	g := sh.groups[key]
	if g == nil {
		g = newGroup(key)
		sh.groups[key] = g
	}
	return g
}

// keep drops the groups no slot belongs to any more.
func (sh *shaper) keep(keys map[string]bool) {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	for k := range sh.groups {
		if !keys[k] {
			delete(sh.groups, k)
		}
	}
}

// setChannel turns the fair share on (the channel in Mbit/s) or off (nil).
func (sh *shaper) setChannel(s *nodeapi.Shaping) {
	var bps int64
	if s != nil && s.ChannelMbps > 0 {
		bps = int64(s.ChannelMbps) * mbit
	}
	sh.channel.Store(bps)
	sh.share()
}

// share sets every group's ceilings: its own cap, and with the fair share on its share of
// the channel, whichever is lower.
func (sh *shaper) share() {
	sh.mu.Lock()
	gs := make([]*group, 0, len(sh.groups))
	for _, g := range sh.groups {
		gs = append(gs, g)
	}
	sh.mu.Unlock()
	channel := float64(sh.channel.Load())
	now := sh.now()
	per := shareEvery.Seconds()
	var ups, downs []used
	for _, g := range gs {
		// What it moved since the last share, as a rate.
		up, down := float64(g.upN.Swap(0))/per, float64(g.downN.Swap(0))/per
		if up+down > 0 {
			g.lastActive.Store(now.UnixNano())
		}
		if channel > 0 && now.Sub(time.Unix(0, g.lastActive.Load())) < activeFor {
			ups = append(ups, used{g, up, float64(g.up.Load())})
			downs = append(downs, used{g, down, float64(g.down.Load())})
		}
	}
	upShare, downShare := fairShares(ups, channel), fairShares(downs, channel)
	for _, g := range gs {
		own := float64(g.own.Load())
		g.up.Store(int64(lower(own, upShare[g])))
		g.down.Store(int64(lower(own, downShare[g])))
	}
}

// used is what a group moved one way in the last period (bytes/s) and its ceiling that
// way (0: none).
type used struct {
	g       *group
	rate    float64
	ceiling float64
}

// lower is the lower of two ceilings, where 0 is none.
func lower(a, b float64) float64 {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	}
	return math.Min(a, b)
}

// fairShares splits channel (bytes/s) between the active groups, max-min: every group gets
// an even share as its ceiling; the groups that hit their ceilings and want more share
// what the others leave unused. A group its own cap holds back wants no more than that.
func fairShares(active []used, channel float64) map[*group]float64 {
	out := make(map[*group]float64, len(active))
	if channel <= 0 || len(active) == 0 {
		return out
	}
	even := math.Max(channel/float64(len(active)), shareFloor)
	left := channel
	var hungry []*group
	for _, u := range active {
		own := float64(u.g.own.Load())
		atCeiling := u.ceiling <= 0 || u.rate >= hungryAt*u.ceiling
		heldByOwn := own > 0 && u.rate >= hungryAt*own
		if atCeiling && !heldByOwn && u.rate >= hungryAt*even {
			hungry = append(hungry, u.g)
			continue
		}
		out[u.g] = even
		left -= math.Min(u.rate, even)
	}
	if len(hungry) > 0 {
		each := math.Max(left/float64(len(hungry)), even)
		for _, g := range hungry {
			out[g] = each
		}
	}
	return out
}

// sameSpeed says whether a ceiling the core holds is close enough to a new one to be left
// as it is: the fair share moves a little every second, and each change is a pass of the
// core over its users.
func sameSpeed(held, want int64) bool {
	if held == want {
		return true
	}
	if held <= 0 || want <= 0 {
		return false
	}
	return math.Abs(float64(held-want)) <= 0.02*float64(want)
}
