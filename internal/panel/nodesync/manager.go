package nodesync

import (
	"context"
	"database/sql"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// LocalNode is the id of the panel's own node, reached over the unix socket.
const LocalNode int64 = 1

// Target is how the panel reaches one node.
type Target struct {
	Node Node
	TLS  TLSSource
	// Local is the panel's own node: only it may use the panel as its REALITY target.
	Local bool
}

// Connect builds the client for a node row; the app knows the socket and certificates.
type Connect func(n db.Node) (Target, error)

// Manager runs a Syncer per node and the panel-wide upkeep: period resets, the slot
// pool, devices. Every node gets the same slots and policies, so one subscription works
// on all of them; traffic and devices add up across nodes.
type Manager struct {
	st      *store.Store
	set     *settings.Settings
	pool    *domain.Pool
	connect Connect
	log     *slog.Logger
	now     func() time.Time

	nodesDirty chan struct{}

	mu        sync.Mutex
	running   map[int64]*running
	lastPurge time.Time
}

type running struct {
	s      *Syncer
	key    string
	cancel context.CancelFunc
	done   chan struct{}
}

func NewManager(st *store.Store, set *settings.Settings, pool *domain.Pool, connect Connect, log *slog.Logger, now func() time.Time) *Manager {
	return &Manager{st: st, set: set, pool: pool, connect: connect, log: log, now: now,
		nodesDirty: make(chan struct{}, 1), running: map[int64]*running{}}
}

func (m *Manager) Run(ctx context.Context) {
	m.reconcile(ctx)
	maintain := time.NewTicker(30 * time.Second)
	defer maintain.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-m.nodesDirty:
			m.reconcile(ctx)
		case <-maintain.C:
			m.maintain(ctx)
		}
	}
}

// PoliciesChanged and SlotsChanged implement domain.Changes for all nodes at once.
func (m *Manager) PoliciesChanged() {
	for _, s := range m.Syncers() {
		s.PoliciesChanged()
	}
}

func (m *Manager) SlotsChanged() {
	for _, s := range m.Syncers() {
		s.SlotsChanged()
	}
}

// NodesChanged restarts syncers after a node was added, removed or re-keyed.
func (m *Manager) NodesChanged() { signal(m.nodesDirty) }

// Syncers returns the running syncers ordered by node id.
func (m *Manager) Syncers() []*Syncer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Syncer, 0, len(m.running))
	for _, r := range m.running {
		out = append(out, r.s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// Syncer returns the syncer of one node.
func (m *Manager) Syncer(id int64) (*Syncer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.running[id]
	if !ok {
		return nil, false
	}
	return r.s, true
}

// Online merges the live view of all nodes: a device on two nodes counts once.
func (m *Manager) Online() map[string]nodeapi.Online {
	out := map[string]nodeapi.Online{}
	for _, s := range m.Syncers() {
		for slot, on := range s.Online() {
			cur := out[slot]
			cur.Conns += on.Conns
			for _, ip := range on.IPs {
				if !slices.Contains(cur.IPs, ip) {
					cur.IPs = append(cur.IPs, ip)
				}
			}
			out[slot] = cur
		}
	}
	return out
}

// otherIPs returns, per slot, the devices online on nodes other than id.
func (m *Manager) otherIPs(id int64) map[string][]string {
	out := map[string][]string{}
	for _, s := range m.Syncers() {
		if s.id == id {
			continue
		}
		for slot, on := range s.Online() {
			for _, ip := range on.IPs {
				if !slices.Contains(out[slot], ip) {
					out[slot] = append(out[slot], ip)
				}
			}
		}
	}
	for _, ips := range out {
		slices.Sort(ips)
	}
	return out
}

// reconcile starts a syncer for every node and restarts it when its address or
// certificate changes.
func (m *Manager) reconcile(ctx context.Context) {
	nodes, err := m.st.Q.ListNodes(ctx)
	if err != nil {
		m.log.Error("list nodes", "err", err)
		return
	}
	want := map[int64]db.Node{}
	for _, n := range nodes {
		want[n.ID] = n
	}
	m.mu.Lock()
	var stop []*running
	for id, r := range m.running {
		if n, ok := want[id]; !ok || nodeKey(n) != r.key {
			stop = append(stop, r)
			delete(m.running, id)
		}
	}
	m.mu.Unlock()
	for _, r := range stop {
		r.cancel()
		<-r.done
	}
	for _, n := range nodes {
		m.mu.Lock()
		_, ok := m.running[n.ID]
		m.mu.Unlock()
		if ok {
			continue
		}
		t, err := m.connect(n)
		if err != nil {
			m.log.Error("connect node", "node", n.ID, "err", err)
			continue
		}
		s := newSyncer(m, n.ID, t)
		sctx, cancel := context.WithCancel(ctx)
		r := &running{s: s, key: nodeKey(n), cancel: cancel, done: make(chan struct{})}
		m.mu.Lock()
		m.running[n.ID] = r
		m.mu.Unlock()
		go func() {
			defer close(r.done)
			s.run(sctx)
		}()
	}
}

func nodeKey(n db.Node) string { return n.Address + "|" + n.CertSha256 }

func (m *Manager) stopAll() {
	m.mu.Lock()
	all := make([]*running, 0, len(m.running))
	for id, r := range m.running {
		all = append(all, r)
		delete(m.running, id)
	}
	m.mu.Unlock()
	for _, r := range all {
		r.cancel()
		<-r.done
	}
}

// Retire tells a node that was removed from the panel to drop its listeners and users.
func (m *Manager) Retire(ctx context.Context, n Node) error {
	_, err := n.Apply(ctx, nodeapi.DesiredState{Inbounds: []nodeapi.Inbound{}, Slots: []nodeapi.Slot{}})
	return err
}

func (m *Manager) maintain(ctx context.Context) {
	now := m.now()
	if err := m.resetPeriods(ctx, now); err != nil {
		m.log.Error("period resets", "err", err)
	}
	m.maintainPool(ctx, now)
	if err := m.st.Q.PruneTrafficHourly(ctx, now.Add(-62*24*time.Hour).Unix()/3600); err != nil {
		m.log.Error("prune traffic", "err", err)
	}
	if err := m.recordDevices(ctx, now); err != nil {
		m.log.Error("record devices", "err", err)
	}
	m.reconcile(ctx)
	syncers := m.Syncers()
	for _, s := range syncers {
		// Reconcile: picks up changes made outside the API (server CLI, restore) and pushes
		// policies, whose key changes by time alone when a user crosses expires_at.
		s.SlotsChanged()
		if len(syncers) > 1 {
			// Each node only sees its own traffic: refresh the quota left after the others.
			s.PoliciesChanged()
		}
	}
}

func (m *Manager) resetPeriods(ctx context.Context, now time.Time) error {
	users, err := m.st.Q.ListUsers(ctx)
	if err != nil {
		return err
	}
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	changed := false
	for _, u := range users {
		start := u.PeriodStart
		switch u.ResetStrategy {
		case "month_start":
			if start >= monthStart {
				continue
			}
			start = monthStart
		case "period":
			length := max(u.PeriodDays, 1) * 86400
			if now.Unix() < start+length {
				continue
			}
			start += (now.Unix() - start) / length * length
		default:
			continue
		}
		if err := m.st.Q.ResetUserTraffic(ctx, db.ResetUserTrafficParams{PeriodStart: start, UpdatedAt: now.Unix(), ID: u.ID}); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		m.PoliciesChanged()
	}
	return nil
}

func (m *Manager) maintainPool(ctx context.Context, now time.Time) {
	stats, err := m.pool.Stats(ctx)
	if err != nil {
		m.log.Error("pool stats", "err", err)
		return
	}
	quietHour, _, err := settings.Get[int](ctx, m.set, "quiet_hour_utc")
	if err != nil {
		m.log.Error("quiet hour", "err", err)
	}
	inQuietHour := now.UTC().Hour() == quietHour && now.Sub(m.lastPurge) > 20*time.Hour
	var refill, purge bool
	switch {
	case stats.Free < domain.CriticalFree:
		refill = true
	case inQuietHour && stats.Free < domain.LowWatermark:
		refill, purge = true, true
	case inQuietHour && stats.Burned > 0:
		purge = true
	}
	if !refill && !purge {
		return
	}
	if purge {
		if err := m.pool.PurgeBurned(ctx); err != nil {
			m.log.Error("purge slots", "err", err)
			return
		}
		m.lastPurge = now
	}
	if refill {
		if err := m.pool.Refill(ctx, domain.RefillBatch); err != nil {
			m.log.Error("refill slots", "err", err)
			return
		}
	}
	m.log.Info("slot pool maintained", "free", stats.Free, "refill", refill, "purge", purge)
	m.SlotsChanged()
}

func (m *Manager) recordDevices(ctx context.Context, now time.Time) error {
	online := m.Online()
	if len(online) == 0 {
		return nil
	}
	rows, err := m.st.Q.ListSlotUsers(ctx)
	if err != nil {
		return err
	}
	owner := make(map[string]int64, len(rows))
	for _, r := range rows {
		owner[r.SlotName] = r.UserID
	}
	return m.st.Tx(ctx, func(q *db.Queries) error {
		for slot, on := range online {
			uid, ok := owner[slot]
			if !ok {
				continue
			}
			if err := q.SetUserOnline(ctx, db.SetUserOnlineParams{OnlineAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: uid}); err != nil {
				return err
			}
			for _, ip := range on.IPs {
				if err := q.UpsertDevice(ctx, db.UpsertDeviceParams{UserID: uid, Ip: ip, FirstSeen: now.Unix(), LastSeen: now.Unix()}); err != nil {
					return err
				}
			}
		}
		return q.PruneDevices(ctx, now.Add(-30*24*time.Hour).Unix())
	})
}

// stateKeyOf names a node_state entry of one node.
func stateKeyOf(name string, id int64) string { return name + "/" + strconv.FormatInt(id, 10) }
