package node

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/nodeapi"
)

// Registry holds the panel's view of each slot on this node: its policy, its devices, its
// quotas and traffic pools, its speed group, and the traffic the core counted for it and
// the panel has not stored yet. The core (core.go) counts the bytes and turns users away;
// the registry adds them up across the inbounds of a slot and tells the core whom to keep
// out.
//
// Lock order: batchMu → mu → slot.mu → limitMu.
type Registry struct {
	mu       sync.RWMutex
	byKey    map[string]*slot // slot name and uuid → slot
	byName   map[string]*slot
	shared   map[string]*slot  // listeners with one key for everyone, by listener name
	poolOf   map[string]string // listener name → its traffic pool, when it has one
	inbounds map[string]bool   // listeners with users, name → their users are UUIDs (TUIC)
	retired  []*slot           // removed slots whose counters are not cut yet
	now      func() time.Time
	release  time.Duration

	batchMu sync.Mutex
	epoch   string
	seq     int64
	pending *nodeapi.Counters

	torrentCfg atomic.Pointer[nodeapi.TorrentBlock] // nil: the torrent blocker is off
	torrent    torrents

	ingress atomic.Pointer[ingress] // nil: anyone may connect

	shaper *shaper // speed caps and the fair share (shaper.go)

	core    core
	limitMu sync.Mutex
	limits  map[userKey]coreLimit // what the core holds users of inbounds to now
}

// coreLimit is what the core holds a user of an inbound to: kept out, and a traffic budget
// (0: none) the core cuts the user at by itself, to the byte. The budget is what was left
// of the quota the inbound counts to when the panel last sent it (gen); the registry, which
// adds the inbounds up once a second, keeps the slot out once their sum runs it out.
type coreLimit struct {
	out    bool
	budget int64
	gen    int64
}

type userKey struct{ inbound, user string }

type slot struct {
	name, uuid string
	shared     bool // a listener's one key for everyone: no devices, no counters

	up, down  atomic.Int64 // counted but not yet cut into a batch
	quotaOn   atomic.Bool
	remaining atomic.Int64
	// The torrent blocker: the slot is let alone, or kept out until the panel's ban or
	// the node's own short one ends (unix seconds).
	torrentExempt atomic.Bool
	policyBan     atomic.Int64
	localBan      atomic.Int64

	mu          sync.Mutex
	allowed     bool
	exhausted   bool            // the main quota: inbounds outside every pool
	budget      int64           // the main quota left when the panel last sent it
	budgetGen   int64           // moves with every quota the panel sends
	inbounds    map[string]bool // nil = all
	deviceLimit int
	otherIPs    map[string]bool // the slot's devices on other nodes of the panel
	// ips are the slot's devices on this node, each until when it holds its place: the
	// core lists an address while it is connected and for the release time after; one
	// just let in holds a short place until the core lists it.
	ips        map[string]time.Time
	online     nodeapi.Online        // as the core saw the slot at the last drain
	seen       map[seenKey]time.Time // last admitted connection per device and inbound
	seenPruned time.Time
	pools      map[string]*bucket // traffic pools of the slot, made on first use
	shaped     bool               // its connections go through the limiter

	group atomic.Pointer[group] // the user's speed group; nil before the first policy
}

type seenKey struct{ ip, inbound string }

// activityKeep bounds how far back Activity reports; the panel looks at the last minutes.
const activityKeep = time.Hour

// seqAfterCrash is how far restore moves the batch counter on: far more batches than the
// counters file can be behind (one is cut at most every couple of seconds, the file is
// written every ten).
const seqAfterCrash = 100_000

// admitHold is how long a device just let in holds its place before the core lists it.
const admitHold = 5 * time.Second

func NewRegistry(epoch string, seq int64, release time.Duration, now func() time.Time, c core) *Registry {
	return &Registry{byKey: map[string]*slot{}, byName: map[string]*slot{}, epoch: epoch, seq: seq, release: release, now: now,
		torrent: newTorrents(), shaper: newShaper(now), core: c, limits: map[userKey]coreLimit{}}
}

func newSlot(name, uuid string) *slot {
	return &slot{name: name, uuid: uuid, ips: map[string]time.Time{}, seen: map[seenKey]time.Time{}}
}

func (r *Registry) Epoch() string {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	return r.epoch
}

// SetSlots replaces the slot set, keeping counters of slots that stay. A removed slot
// leaves the listeners' users on the next patch, and the core closes its connections.
func (r *Registry) SetSlots(list []nodeapi.Slot) {
	r.mu.Lock()
	byName := make(map[string]*slot, len(list))
	byKey := make(map[string]*slot, 2*len(list))
	for _, sl := range list {
		s := r.byName[sl.Name]
		if s == nil || s.uuid != sl.UUID {
			s = newSlot(sl.Name, sl.UUID)
		}
		byName[sl.Name] = s
		byKey[sl.Name] = s
		byKey[sl.UUID] = s
		// The core gives TUIC users in lower case.
		byKey[strings.ToLower(sl.UUID)] = s
	}
	for name, s := range r.byName {
		if byName[name] != s {
			r.retired = append(r.retired, s)
		}
	}
	r.byName, r.byKey = byName, byKey
	r.mu.Unlock()
	r.reconcile()
}

// SetInbounds names the listeners whose users are slots; byUUID marks those that know
// them by UUID (TUIC).
func (r *Registry) SetInbounds(byUUID map[string]bool) {
	r.mu.Lock()
	r.inbounds = byUUID
	r.mu.Unlock()
	r.reconcile()
}

// SetPolicies applies access rules. Slots without a policy are denied.
func (r *Registry) SetPolicies(epoch string, list []nodeapi.Policy) {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	// The quotas left are the panel's minus what the node counted since: take in what the
	// core counted until now first.
	r.drain()
	given := make(map[string]nodeapi.Policy, len(list))
	for _, p := range list {
		given[p.Slot] = p
	}
	r.mu.RLock()
	slots := make([]*slot, 0, len(r.byName))
	for _, s := range r.byName {
		slots = append(slots, s)
	}
	r.mu.RUnlock()

	groups := map[string]bool{}
	for _, s := range slots {
		p, ok := given[s.name]
		key := s.name
		if ok && p.Group != "" {
			key = p.Group
		}
		g := r.shaper.group(key)
		groups[key] = true
		var own int64
		if ok && p.SpeedMbps > 0 {
			own = int64(p.SpeedMbps) * mbit
		}
		g.own.Store(own)
		s.group.Store(g)
		s.mu.Lock()
		s.allowed = ok && p.Allowed
		s.inbounds = nil
		s.deviceLimit = 0
		s.otherIPs = nil
		s.exhausted = false
		s.quotaOn.Store(false)
		s.budget = 0
		s.budgetGen++
		s.torrentExempt.Store(ok && p.TorrentExempt)
		now := r.now().Unix()
		// A ban the panel held and no longer does was lifted there: the node's own short ban
		// for the same catch goes with it.
		if s.policyBan.Load() > now && (!ok || p.BannedUntil <= now) {
			s.localBan.Store(0)
		}
		s.policyBan.Store(0)
		if ok {
			s.policyBan.Store(p.BannedUntil)
			if len(p.Inbounds) > 0 {
				s.inbounds = make(map[string]bool, len(p.Inbounds))
				for _, in := range p.Inbounds {
					s.inbounds[in] = true
				}
			}
			s.deviceLimit = p.DeviceLimit
			if len(p.OtherIPs) > 0 {
				s.otherIPs = make(map[string]bool, len(p.OtherIPs))
				for _, ip := range p.OtherIPs {
					s.otherIPs[ip] = true
				}
			}
			if p.QuotaRemaining >= 0 {
				// Bytes the panel has not seen yet as of BaseSeq still count against the quota.
				after := s.up.Load() + s.down.Load()
				if r.pending != nil && (epoch != r.epoch || r.pending.Seq > p.BaseSeq) {
					t := r.pending.Slots[s.name]
					after += t.Up + t.Down
				}
				rem := p.QuotaRemaining - after
				s.remaining.Store(rem)
				s.quotaOn.Store(true)
				s.exhausted = rem <= 0
				s.budget = max(rem, 0)
			}
		}
		// Pools: a pool the policy does not name has no limit here.
		for _, b := range s.pools {
			b.quotaOn.Store(false)
			b.exhausted.Store(false)
		}
		if ok {
			for _, pq := range p.Pools {
				if pq.Remaining < 0 {
					continue
				}
				b := s.poolLocked(pq.Pool)
				after := b.up.Load() + b.down.Load()
				if r.pending != nil && (epoch != r.epoch || r.pending.Seq > p.BaseSeq) {
					t := r.pending.Pools[s.name][pq.Pool]
					after += t.Up + t.Down
				}
				rem := pq.Remaining - after
				b.remaining.Store(rem)
				b.quotaOn.Store(true)
				b.exhausted.Store(rem <= 0)
				b.budget = max(rem, 0)
			}
		}
		s.mu.Unlock()
	}
	r.shaper.keep(groups)
	r.shaper.share()
	r.reconcile()
	r.reshape()
}

// SetShaping turns the node's fair share on or off.
func (r *Registry) SetShaping(s *nodeapi.Shaping) {
	r.shaper.setChannel(s)
	r.reshape()
}

// Run takes in the core's counters, keeps the core's list of whom to turn away up to date
// (bans run out by themselves) and works the fair share out, every second until ctx ends.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(shareEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			r.drain()
			r.reconcile()
			r.shaper.share()
		case <-ctx.Done():
			return
		}
	}
}

// shapedGroup is the group whose ceilings hold the slot's new connections, nil when
// nothing holds them back (no cap, no fair share): they may then be spliced.
func (r *Registry) shapedGroup(s *slot) *group {
	g := s.group.Load()
	if g == nil || s.shared || (g.own.Load() == 0 && !r.shaper.on()) {
		return nil
	}
	return g
}

// reshape closes a slot's connections when a cap or the fair share comes in for it: they
// were opened around the limiter, and the apps open them again at once, held this time.
func (r *Registry) reshape() {
	r.mu.RLock()
	slots := values(r.byName)
	inbounds := r.inbounds
	r.mu.RUnlock()
	for _, s := range slots {
		shaped := r.shapedGroup(s) != nil
		s.mu.Lock()
		was := s.shaped
		s.shaped = shaped
		s.mu.Unlock()
		if shaped && !was {
			for in, byUUID := range inbounds {
				r.core.closeUser(in, coreUser(s, byUUID))
			}
		}
	}
}

func (r *Registry) lookup(key string) *slot {
	if key == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

// SetShared names the listeners whose clients carry no user: one key for everyone
// (Shadowsocks-2022, Sudoku, Snell). Their connections go through without a policy or
// limits, and are not counted: no user owns the traffic and the panel has no use for a
// listener's total.
func (r *Registry) SetShared(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*slot, len(names))
	for _, n := range names {
		s := r.shared[n]
		if s == nil {
			s = newSlot("~"+n, "")
			s.shared = true
			s.allowed = true
		}
		next[n] = s
	}
	r.shared = next
}

func (r *Registry) sharedSlot(inName string) *slot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.shared[inName]
}

// admitIn lets a connection of a user in, or says no (nil): the slot is kept out of the
// inbound, or the device would be one over the limit. A device already counted on
// another node is not a new one.
func (r *Registry) admitIn(user, inName, ip string) *slot {
	s := r.lookup(user)
	if user == "" {
		s = r.sharedSlot(inName)
	}
	if s == nil {
		return nil
	}
	if s.shared {
		return s
	}
	now := r.now()
	pool := r.poolOfListener(inName)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deniedLocked(inName, pool, now) {
		return nil
	}
	if until, ok := s.ips[ip]; !ok || !until.After(now) {
		if s.deviceLimit > 0 && !s.otherIPs[ip] && s.devicesLocked(now) >= s.deviceLimit {
			return nil
		}
	}
	if until := now.Add(admitHold); until.After(s.ips[ip]) {
		s.ips[ip] = until
	}
	s.seen[seenKey{ip, inName}] = now
	if len(s.seen) > 256 && now.Sub(s.seenPruned) >= time.Minute {
		// Activity prunes too, but a panel that never asks must not grow it forever.
		for k, t := range s.seen {
			if now.Sub(t) > activityKeep {
				delete(s.seen, k)
			}
		}
		s.seenPruned = now
	}
	return s
}

// deniedLocked says whether the slot is kept out of an inbound (in traffic pool pool, ""
// for none): not allowed, banned, not one of its inbounds, or the quota the inbound
// counts to has run out. An exhausted pool keeps the slot out of its own inbounds only,
// and so does the main quota.
func (s *slot) deniedLocked(inName, pool string, now time.Time) bool {
	switch {
	case !s.allowed, s.banned(now), s.inbounds != nil && !s.inbounds[inName]:
		return true
	case pool != "":
		b := s.pools[pool]
		return b != nil && b.exhausted.Load()
	}
	return s.exhausted
}

// reconcile tells the core what to hold each slot to on each inbound: kept out where it is
// denied (the core closes its open connections and turns new ones away), and the budget
// of the quota the inbound counts to.
func (r *Registry) reconcile() {
	now := r.now()
	want := map[userKey]coreLimit{}
	r.mu.RLock()
	for _, s := range r.byName {
		s.mu.Lock()
		for in, byUUID := range r.inbounds {
			pool := r.poolOf[in]
			l := coreLimit{out: s.deniedLocked(in, pool, now), gen: s.budgetGen}
			switch {
			case pool == "" && s.quotaOn.Load():
				l.budget = s.budget
			case pool != "":
				if b := s.pools[pool]; b != nil && b.quotaOn.Load() {
					l.budget = b.budget
				}
			}
			if l.out || l.budget > 0 {
				want[userKey{in, coreUser(s, byUUID)}] = l
			}
		}
		s.mu.Unlock()
	}
	r.mu.RUnlock()
	r.limitMu.Lock()
	defer r.limitMu.Unlock()
	for k := range r.limits {
		if _, ok := want[k]; !ok {
			r.core.limit(k.inbound, k.user, false, 0, false)
		}
	}
	for k, l := range want {
		old, had := r.limits[k]
		if had && old.out == l.out && (l.budget == 0 || old.budget == l.budget && old.gen == l.gen) {
			continue
		}
		// A budget the panel sent anew counts from now; one kept counts on.
		fresh := l.budget > 0 && (!had || old.gen != l.gen || old.budget != l.budget)
		r.core.limit(k.inbound, k.user, l.out, l.budget, fresh)
	}
	r.limits = want
}

// drain adds what the core counted since the last drain to the slots and their pools,
// and takes in who is online. A quota that runs out is kept out by the next reconcile.
func (r *Registry) drain() {
	stats := r.core.drain()
	now := r.now()
	type seen struct {
		ips   map[string]time.Time
		conns int
	}
	online := map[*slot]*seen{}
	r.mu.RLock()
	for _, st := range stats {
		if _, ok := r.inbounds[st.Inbound]; !ok {
			continue // the relay, whose users are other nodes
		}
		s := r.byKey[st.User]
		if s == nil {
			s = r.retiredLocked(st.User)
		}
		if s == nil || s.shared {
			continue
		}
		if st.Up != 0 || st.Down != 0 {
			s.countIn(r.poolOf[st.Inbound], st.Up, st.Down)
		}
		o := online[s]
		if o == nil {
			o = &seen{ips: map[string]time.Time{}}
			online[s] = o
		}
		o.conns += int(st.Conns)
		for _, ip := range st.IPs {
			// Connected: holds its place until the next drain at least. Gone: for what is
			// left of the release time.
			until := now.Add(2 * shareEvery)
			if ip.Conns == 0 {
				until = ip.LastSeen.Add(r.release)
			}
			if until.After(o.ips[ip.IP]) {
				o.ips[ip.IP] = until
			}
		}
	}
	slots := values(r.byName)
	r.mu.RUnlock()
	for _, s := range slots {
		o := online[s]
		s.mu.Lock()
		for ip, until := range s.ips {
			if !until.After(now) {
				delete(s.ips, ip)
			}
		}
		s.online = nodeapi.Online{}
		if o != nil {
			for ip, until := range o.ips {
				if !until.After(now) {
					continue
				}
				if until.After(s.ips[ip]) {
					s.ips[ip] = until
				}
				s.online.IPs = append(s.online.IPs, ip)
			}
			sort.Strings(s.online.IPs)
			s.online.Conns = o.conns
		}
		s.mu.Unlock()
	}
}

// retiredLocked is a removed slot by its name or UUID, still owed its last traffic; r.mu
// is held.
func (r *Registry) retiredLocked(user string) *slot {
	for _, s := range r.retired {
		if s.name == user || strings.EqualFold(s.uuid, user) {
			return s
		}
	}
	return nil
}

// Activity reports, per device, when each inbound last let it in. The panel compares
// inbounds: a device that keeps reaching the node's other inbounds but not one of them is
// cut off from that one on the way (a port blocked by DPI), not idle.
func (r *Registry) Activity() nodeapi.Activity {
	r.mu.RLock()
	slots := values(r.byName)
	r.mu.RUnlock()
	cutoff := r.now().Add(-activityKeep)
	type device struct{ slot, ip string }
	byDevice := map[device]*nodeapi.ClientActivity{}
	for _, s := range slots {
		s.mu.Lock()
		for k, t := range s.seen {
			if t.Before(cutoff) {
				delete(s.seen, k)
				continue
			}
			dk := device{s.name, k.ip}
			c := byDevice[dk]
			if c == nil {
				c = &nodeapi.ClientActivity{Slot: s.name, IP: k.ip, Seen: map[string]int64{}}
				byDevice[dk] = c
			}
			c.Seen[k.inbound] = t.Unix()
		}
		s.mu.Unlock()
	}
	out := nodeapi.Activity{Clients: make([]nodeapi.ClientActivity, 0, len(byDevice))}
	for _, c := range byDevice {
		out.Clients = append(out.Clients, *c)
	}
	sort.Slice(out.Clients, func(i, j int) bool {
		a, b := out.Clients[i], out.Clients[j]
		return a.Slot < b.Slot || a.Slot == b.Slot && a.IP < b.IP
	})
	return out
}

func (s *slot) count(up, down int64) {
	if s.shared {
		return
	}
	if up != 0 {
		s.up.Add(up)
	}
	if down != 0 {
		s.down.Add(down)
	}
	if s.quotaOn.Load() && s.remaining.Add(-(up+down)) <= 0 {
		s.exhaust()
	}
}

// exhaust marks the main quota run out; the next reconcile keeps the slot out of the
// inbounds that count to it, while connections to a pool keep going.
func (s *slot) exhaust() {
	s.mu.Lock()
	s.exhausted = true
	s.mu.Unlock()
}

// Counters returns the outstanding batch, cutting a new one if the previous was acked.
func (r *Registry) Counters() nodeapi.Counters {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	r.drain()
	r.reconcile()
	r.mu.Lock()
	all := make([]*slot, 0, len(r.byName)+len(r.retired))
	for _, s := range r.byName {
		all = append(all, s)
	}
	if r.pending == nil {
		all = append(all, r.retired...)
		r.retired = nil
	}
	r.mu.Unlock()

	batch := r.pending
	if batch == nil {
		b := &nodeapi.Counters{Epoch: r.epoch, Seq: r.seq + 1, Slots: map[string]nodeapi.Traffic{}}
		for _, s := range all {
			up, down := s.up.Swap(0), s.down.Swap(0)
			if up != 0 || down != 0 {
				t := b.Slots[s.name]
				b.Slots[s.name] = nodeapi.Traffic{Up: t.Up + up, Down: t.Down + down}
			}
			s.mu.Lock()
			for pool, pb := range s.pools {
				up, down := pb.up.Swap(0), pb.down.Swap(0)
				if up == 0 && down == 0 {
					continue
				}
				if b.Pools == nil {
					b.Pools = map[string]map[string]nodeapi.Traffic{}
				}
				if b.Pools[s.name] == nil {
					b.Pools[s.name] = map[string]nodeapi.Traffic{}
				}
				t := b.Pools[s.name][pool]
				b.Pools[s.name][pool] = nodeapi.Traffic{Up: t.Up + up, Down: t.Down + down}
			}
			s.mu.Unlock()
		}
		// A batch without traffic is not cut: it would cost the panel a transaction every
		// couple of seconds to learn nothing. The reply says Idle, so the panel keeps to
		// the live view; an older panel takes it for the last batch again and drops it.
		if len(b.Slots) > 0 || len(b.Pools) > 0 {
			r.seq = b.Seq
			r.pending = b
		} else {
			b.Seq, b.Idle = r.seq, true
		}
		batch = b
	}
	out := *batch
	out.Online = map[string]nodeapi.Online{}
	for _, s := range all {
		s.mu.Lock()
		o := s.online
		s.mu.Unlock()
		if o.Conns > 0 || len(o.IPs) > 0 {
			out.Online[s.name] = o
		}
	}
	return out
}

// Ack drops the outstanding batch once the panel has stored it.
func (r *Registry) Ack(epoch string, seq int64) bool {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	if epoch != r.epoch || r.pending == nil || r.pending.Seq != seq {
		return false
	}
	r.pending = nil
	return true
}

// ConnCount is the users' open connections and UDP sessions as of the last drain.
func (r *Registry) ConnCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, s := range r.byName {
		s.mu.Lock()
		n += s.online.Conns
		s.mu.Unlock()
	}
	return n
}

// counterState is what survives a node restart.
type counterState struct {
	Epoch   string                     `json:"epoch"`
	Seq     int64                      `json:"seq"`
	Pending *nodeapi.Counters          `json:"pending,omitempty"`
	Current map[string]nodeapi.Traffic `json:"current"`
	// CurrentPools: the same for traffic pools, slot → pool.
	CurrentPools map[string]map[string]nodeapi.Traffic `json:"current_pools,omitempty"`
}

func (r *Registry) snapshot() counterState {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	st := counterState{Epoch: r.epoch, Seq: r.seq, Pending: r.pending, Current: map[string]nodeapi.Traffic{}}
	r.mu.RLock()
	for _, s := range append(r.retired[:len(r.retired):len(r.retired)], values(r.byName)...) {
		if up, down := s.up.Load(), s.down.Load(); up != 0 || down != 0 {
			t := st.Current[s.name]
			st.Current[s.name] = nodeapi.Traffic{Up: t.Up + up, Down: t.Down + down}
		}
		s.mu.Lock()
		for pool, b := range s.pools {
			if up, down := b.up.Load(), b.down.Load(); up != 0 || down != 0 {
				if st.CurrentPools == nil {
					st.CurrentPools = map[string]map[string]nodeapi.Traffic{}
				}
				if st.CurrentPools[s.name] == nil {
					st.CurrentPools[s.name] = map[string]nodeapi.Traffic{}
				}
				t := st.CurrentPools[s.name][pool]
				st.CurrentPools[s.name][pool] = nodeapi.Traffic{Up: t.Up + up, Down: t.Down + down}
			}
		}
		s.mu.Unlock()
	}
	r.mu.RUnlock()
	return st
}

// restore adds persisted counters to the current slots; unknown slots are kept as retired.
func (r *Registry) restore(st counterState) {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	// The file is written every few seconds, so after a crash it is behind the batches the
	// panel has stored since. The panel drops a batch whose seq it has seen already: skip
	// ahead of anything the node could have cut, or the first traffic after a crash would
	// be thrown away as a duplicate. A pending batch keeps its own seq: the panel treats
	// it as the duplicate or the news it is.
	r.epoch, r.seq, r.pending = st.Epoch, st.Seq+seqAfterCrash, st.Pending
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, t := range st.Current {
		s := r.byName[name]
		if s == nil {
			s = newSlot(name, "")
			r.retired = append(r.retired, s)
		}
		s.up.Add(t.Up)
		s.down.Add(t.Down)
	}
	for name, pools := range st.CurrentPools {
		s := r.byName[name]
		if s == nil {
			s = newSlot(name, "")
			r.retired = append(r.retired, s)
		}
		s.mu.Lock()
		for pool, t := range pools {
			b := s.poolLocked(pool)
			b.up.Add(t.Up)
			b.down.Add(t.Down)
		}
		s.mu.Unlock()
	}
}

func values(m map[string]*slot) []*slot {
	out := make([]*slot, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

// devicesLocked counts the slot's devices on this node and on the panel's other nodes.
func (s *slot) devicesLocked(now time.Time) int {
	n := 0
	for _, until := range s.ips {
		if until.After(now) {
			n++
		}
	}
	for ip := range s.otherIPs {
		if until, ok := s.ips[ip]; !ok || !until.After(now) {
			n++
		}
	}
	return n
}

// bucket counts one traffic pool of a slot: its own bytes and its own quota.
type bucket struct {
	up, down  atomic.Int64
	quotaOn   atomic.Bool
	remaining atomic.Int64
	exhausted atomic.Bool
	budget    int64 // what was left when the panel last sent the quota; slot.mu
}

// poolLocked is the slot's bucket of pool, made on first use; s.mu is held.
func (s *slot) poolLocked(pool string) *bucket {
	if s.pools == nil {
		s.pools = map[string]*bucket{}
	}
	b := s.pools[pool]
	if b == nil {
		b = &bucket{}
		s.pools[pool] = b
	}
	return b
}

// SetPools says which listeners count to which traffic pool; the rest count to the
// main quota.
func (r *Registry) SetPools(poolOf map[string]string) {
	r.mu.Lock()
	r.poolOf = poolOf
	r.mu.Unlock()
}

func (r *Registry) poolOfListener(inName string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.poolOf[inName]
}

// countIn counts bytes to the slot's traffic pool ("": the main quota) and marks the
// quota run out when it does.
func (s *slot) countIn(pool string, up, down int64) {
	if s.shared {
		return
	}
	if g := s.group.Load(); g != nil {
		g.moved(up, down)
	}
	if pool == "" {
		s.count(up, down)
		return
	}
	s.mu.Lock()
	b := s.poolLocked(pool)
	s.mu.Unlock()
	if up != 0 {
		b.up.Add(up)
	}
	if down != 0 {
		b.down.Add(down)
	}
	if b.quotaOn.Load() && b.remaining.Add(-(up+down)) <= 0 {
		b.exhausted.Store(true)
	}
}
