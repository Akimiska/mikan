package node

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/nodeapi"
)

// Registry holds per-slot policy, traffic counters and live connections.
//
// Lock order: batchMu → mu → slot.mu. Connections are always closed after all locks
// are released: closing unregisters the conn, which takes slot.mu again.
type Registry struct {
	mu      sync.RWMutex
	byKey   map[string]*slot // slot name and uuid → slot
	byName  map[string]*slot
	shared  map[string]*slot // listeners with one key for everyone, by listener name
	retired []*slot          // removed slots whose counters are not cut yet
	now     func() time.Time
	release time.Duration

	batchMu sync.Mutex
	epoch   string
	seq     int64
	pending *nodeapi.Counters
}

type slot struct {
	name, uuid string

	up, down  atomic.Int64 // counted but not yet cut into a batch
	quotaOn   atomic.Bool
	remaining atomic.Int64
	blocked   atomic.Bool // !allowed || exhausted; read on every connection

	mu          sync.Mutex
	allowed     bool
	exhausted   bool
	inbounds    map[string]bool // nil = all
	deviceLimit int
	otherIPs    map[string]bool // the slot's devices on other nodes of the panel
	conns       map[*countingConn]struct{}
	ips         map[string]*ipUse
	seen        map[seenKey]time.Time // last admitted connection per device and inbound
}

type ipUse struct {
	open     int
	lastSeen time.Time
}

type seenKey struct{ ip, inbound string }

// activityKeep bounds how far back Activity reports; the panel looks at the last minutes.
const activityKeep = time.Hour

func NewRegistry(epoch string, seq int64, release time.Duration, now func() time.Time) *Registry {
	return &Registry{byKey: map[string]*slot{}, byName: map[string]*slot{}, epoch: epoch, seq: seq, release: release, now: now}
}

func newSlot(name, uuid string) *slot {
	s := &slot{name: name, uuid: uuid, conns: map[*countingConn]struct{}{}, ips: map[string]*ipUse{}, seen: map[seenKey]time.Time{}}
	s.blocked.Store(true) // no policy yet → deny
	return s
}

func (r *Registry) Epoch() string {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	return r.epoch
}

// SetSlots replaces the slot set, keeping counters and connections of slots that stay.
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
	}
	var gone []*countingConn
	for name, s := range r.byName {
		if byName[name] == s {
			continue
		}
		r.retired = append(r.retired, s)
		s.mu.Lock()
		s.allowed = false
		s.blocked.Store(true)
		gone = append(gone, s.connsLocked()...)
		s.mu.Unlock()
	}
	r.byName, r.byKey = byName, byKey
	r.mu.Unlock()
	closeAll(gone)
}

// SetPolicies applies access rules. Slots without a policy are denied.
func (r *Registry) SetPolicies(epoch string, list []nodeapi.Policy) {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
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

	var toClose []*countingConn
	for _, s := range slots {
		p, ok := given[s.name]
		s.mu.Lock()
		s.allowed = ok && p.Allowed
		s.inbounds = nil
		s.deviceLimit = 0
		s.otherIPs = nil
		s.exhausted = false
		s.quotaOn.Store(false)
		if ok {
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
			}
		}
		s.blocked.Store(!s.allowed || s.exhausted)
		if s.blocked.Load() {
			toClose = append(toClose, s.connsLocked()...)
		} else if s.inbounds != nil {
			for c := range s.conns {
				if !s.inbounds[c.inName] {
					toClose = append(toClose, c)
				}
			}
		}
		s.mu.Unlock()
	}
	closeAll(toClose)
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
// limits; the traffic is counted to the listener, which no user owns.
func (r *Registry) SetShared(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make(map[string]*slot, len(names))
	for _, n := range names {
		s := r.shared[n]
		if s == nil {
			s = newSlot("~"+n, "")
			s.allowed = true
			s.blocked.Store(false)
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

// admit checks policy and the device limit. For TCP it reserves an open-connection
// slot on the source IP; release it with slot.closeConn.
func (r *Registry) admit(user, inName, ip string, tcp bool) *slot {
	s := r.lookup(user)
	if user == "" {
		s = r.sharedSlot(inName)
	}
	if s == nil || s.blocked.Load() {
		return nil
	}
	now := r.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.allowed || s.exhausted || (s.inbounds != nil && !s.inbounds[inName]) {
		return nil
	}
	for k, u := range s.ips {
		if u.open == 0 && now.Sub(u.lastSeen) > r.release {
			delete(s.ips, k)
		}
	}
	u := s.ips[ip]
	if u == nil {
		// A device already counted on another node is not a new one.
		if s.deviceLimit > 0 && !s.otherIPs[ip] && s.devicesLocked() >= s.deviceLimit {
			return nil
		}
		u = &ipUse{}
		s.ips[ip] = u
	}
	u.lastSeen = now
	if tcp {
		u.open++
	}
	s.seen[seenKey{ip, inName}] = now
	if len(s.seen) > 256 {
		// Activity prunes too, but a panel that never asks must not grow it forever.
		for k, t := range s.seen {
			if now.Sub(t) > activityKeep {
				delete(s.seen, k)
			}
		}
	}
	return s
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

func (s *slot) addConn(c *countingConn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *slot) closeConn(c *countingConn, now time.Time) {
	s.mu.Lock()
	delete(s.conns, c)
	if u := s.ips[c.ip]; u != nil {
		if u.open > 0 {
			u.open--
		}
		u.lastSeen = now
	}
	s.mu.Unlock()
}

func (s *slot) count(up, down int64) {
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

func (s *slot) exhaust() {
	s.mu.Lock()
	if s.exhausted {
		s.mu.Unlock()
		return
	}
	s.exhausted = true
	s.blocked.Store(true)
	conns := s.connsLocked()
	s.mu.Unlock()
	// Called from inside a conn's Read/Write path: close asynchronously.
	go closeAll(conns)
}

func (s *slot) connsLocked() []*countingConn {
	out := make([]*countingConn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

func closeAll(conns []*countingConn) {
	for _, c := range conns {
		_ = c.Close()
	}
}

// Kick closes the active connections of the given slots. Access is not changed.
func (r *Registry) Kick(names []string) {
	var toClose []*countingConn
	for _, n := range names {
		if s := r.lookup(n); s != nil {
			s.mu.Lock()
			toClose = append(toClose, s.connsLocked()...)
			s.mu.Unlock()
		}
	}
	closeAll(toClose)
}

// Counters returns the outstanding batch, cutting a new one if the previous was acked.
func (r *Registry) Counters() nodeapi.Counters {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
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

	if r.pending == nil {
		r.seq++
		b := &nodeapi.Counters{Epoch: r.epoch, Seq: r.seq, Slots: map[string]nodeapi.Traffic{}}
		for _, s := range all {
			up, down := s.up.Swap(0), s.down.Swap(0)
			if up != 0 || down != 0 {
				t := b.Slots[s.name]
				b.Slots[s.name] = nodeapi.Traffic{Up: t.Up + up, Down: t.Down + down}
			}
		}
		r.pending = b
	}
	out := *r.pending
	out.Online = map[string]nodeapi.Online{}
	now := r.now()
	for _, s := range all {
		s.mu.Lock()
		var ips []string
		for ip, u := range s.ips {
			if u.open > 0 || now.Sub(u.lastSeen) <= r.release {
				ips = append(ips, ip)
			}
		}
		n := len(s.conns)
		s.mu.Unlock()
		if n > 0 || len(ips) > 0 {
			sort.Strings(ips)
			out.Online[s.name] = nodeapi.Online{IPs: ips, Conns: n}
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

func (r *Registry) ConnCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := 0
	for _, s := range r.byName {
		s.mu.Lock()
		n += len(s.conns)
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
	}
	r.mu.RUnlock()
	return st
}

// restore adds persisted counters to the current slots; unknown slots are kept as retired.
func (r *Registry) restore(st counterState) {
	r.batchMu.Lock()
	defer r.batchMu.Unlock()
	r.epoch, r.seq, r.pending = st.Epoch, st.Seq, st.Pending
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
}

func values(m map[string]*slot) []*slot {
	out := make([]*slot, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

// devicesLocked counts the slot's devices on this node and on the panel's other nodes.
func (s *slot) devicesLocked() int {
	n := len(s.ips)
	for ip := range s.otherIPs {
		if s.ips[ip] == nil {
			n++
		}
	}
	return n
}
