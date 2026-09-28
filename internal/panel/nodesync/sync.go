// Package nodesync keeps the node in line with the database: desired state, access
// policies, traffic counters, period resets and the slot pool.
package nodesync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Node is the subset of the node API the syncer needs.
type Node interface {
	Apply(ctx context.Context, s nodeapi.DesiredState) (nodeapi.ApplyResult, error)
	SetPolicies(ctx context.Context, epoch string, p []nodeapi.Policy) error
	Counters(ctx context.Context) (nodeapi.Counters, error)
	Ack(ctx context.Context, epoch string, seq int64) error
	Health(ctx context.Context) (nodeapi.Health, error)
}

// TLSSource returns the certificate the node uses for Hysteria2/TUIC.
type TLSSource func() (*nodeapi.TLSFiles, error)

type Syncer struct {
	st   *store.Store
	set  *settings.Settings
	pool *domain.Pool
	node Node
	tls  TLSSource
	log  *slog.Logger
	now  func() time.Time

	policiesDirty chan struct{}
	stateDirty    chan struct{}

	mu          sync.Mutex
	stateKey    string
	policyKey   string
	lastApplied nodeapi.ApplyResult
	lastPurge   time.Time

	health atomic.Pointer[HealthView]
	online atomic.Pointer[map[string]nodeapi.Online]
}

type HealthView struct {
	OK        bool
	Error     string
	Health    nodeapi.Health
	Listeners []nodeapi.ListenerStatus
	CheckedAt time.Time
}

func New(st *store.Store, set *settings.Settings, pool *domain.Pool, node Node, tls TLSSource, log *slog.Logger, now func() time.Time) *Syncer {
	s := &Syncer{st: st, set: set, pool: pool, node: node, tls: tls, log: log, now: now,
		policiesDirty: make(chan struct{}, 1), stateDirty: make(chan struct{}, 1)}
	empty := map[string]nodeapi.Online{}
	s.online.Store(&empty)
	s.health.Store(&HealthView{Error: "not checked yet"})
	return s
}

func (s *Syncer) PoliciesChanged() { signal(s.policiesDirty) }
func (s *Syncer) SlotsChanged()    { signal(s.stateDirty) }

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Syncer) Health() HealthView { return *s.health.Load() }

// Online returns the live connection view keyed by slot name.
func (s *Syncer) Online() map[string]nodeapi.Online { return *s.online.Load() }

func (s *Syncer) Run(ctx context.Context) {
	counters := time.NewTicker(2 * time.Second)
	maintain := time.NewTicker(30 * time.Second)
	health := time.NewTicker(5 * time.Second)
	defer counters.Stop()
	defer maintain.Stop()
	defer health.Stop()
	s.applyState(ctx)
	s.refreshHealth(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stateDirty:
			s.applyState(ctx)
		case <-s.policiesDirty:
			// Coalesce bursts (bulk actions) into one push.
			time.Sleep(150 * time.Millisecond)
			s.pushPolicies(ctx, true)
		case <-counters.C:
			s.pullCounters(ctx)
		case <-maintain.C:
			s.maintain(ctx)
		case <-health.C:
			s.refreshHealth(ctx)
		}
	}
}

// desired builds the full node state from the database.
func (s *Syncer) desired(ctx context.Context) (nodeapi.DesiredState, error) {
	var st nodeapi.DesiredState
	inbounds, err := s.st.Q.ListInbounds(ctx)
	if err != nil {
		return st, err
	}
	for _, in := range inbounds {
		if in.Enabled == 0 {
			continue
		}
		st.Inbounds = append(st.Inbounds, nodeapi.Inbound{Name: in.Name, Preset: in.Preset, Port: in.Port, Settings: json.RawMessage(in.Settings)})
	}
	slots, err := s.st.Q.ListSlots(ctx)
	if err != nil {
		return st, err
	}
	for _, sl := range slots {
		st.Slots = append(st.Slots, nodeapi.Slot{Name: sl.Name, UUID: sl.Uuid, Secret: sl.Secret})
	}
	if st.TLS, err = s.tls(); err != nil {
		return st, err
	}
	st.Epoch, st.Policies, _, err = s.policies(ctx)
	return st, err
}

func (s *Syncer) applyState(ctx context.Context) {
	st, err := s.desired(ctx)
	if err != nil {
		s.log.Error("build node state", "err", err)
		return
	}
	key := stateKey(st)
	s.mu.Lock()
	same := key == s.stateKey
	s.mu.Unlock()
	if same {
		s.pushPolicies(ctx, false)
		return
	}
	rev, err := s.nextRevision(ctx)
	if err != nil {
		s.log.Error("revision", "err", err)
		return
	}
	st.Revision = rev
	res, err := s.node.Apply(ctx, st)
	if err != nil {
		s.log.Warn("apply node state", "err", err)
		return
	}
	for _, l := range res.Listeners {
		if !l.OK {
			s.log.Error("listener failed", "name", l.Name, "err", l.Error)
		}
	}
	s.mu.Lock()
	s.stateKey = key
	s.policyKey = policyKey(st.Policies)
	s.lastApplied = res
	s.mu.Unlock()
	s.log.Info("node state applied", "revision", rev, "recreated", res.Recreated)
}

func (s *Syncer) pushPolicies(ctx context.Context, force bool) {
	epoch, ps, _, err := s.policies(ctx)
	if err != nil {
		s.log.Error("build policies", "err", err)
		return
	}
	key := policyKey(ps)
	s.mu.Lock()
	unchanged := key == s.policyKey && !force
	s.mu.Unlock()
	if unchanged {
		return
	}
	if err := s.node.SetPolicies(ctx, epoch, ps); err != nil {
		s.log.Warn("push policies", "err", err)
		return
	}
	s.mu.Lock()
	s.policyKey = key
	s.mu.Unlock()
}

func (s *Syncer) policies(ctx context.Context) (epoch string, out []nodeapi.Policy, slotUser map[string]int64, err error) {
	epoch, seq, err := s.countersPos(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	users, err := s.st.Q.ListUsers(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	slots, err := s.st.Q.ListSlots(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	inbounds, err := s.st.Q.ListInbounds(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	slotName := make(map[int64]string, len(slots))
	for _, sl := range slots {
		slotName[sl.ID] = sl.Name
	}
	inboundName := make(map[int64]string, len(inbounds))
	for _, in := range inbounds {
		inboundName[in.ID] = in.Name
	}
	now := s.now()
	slotUser = map[string]int64{}
	for _, u := range users {
		if !u.SlotID.Valid {
			continue
		}
		name := slotName[u.SlotID.Int64]
		slotUser[name] = u.ID
		p := nodeapi.Policy{Slot: name, Allowed: domain.CanConnect(domain.State(u, now)), QuotaRemaining: -1, BaseSeq: seq}
		if u.DeviceLimit.Valid {
			p.DeviceLimit = int(u.DeviceLimit.Int64)
		}
		if u.TrafficLimit.Valid {
			p.QuotaRemaining = max(0, u.TrafficLimit.Int64-u.UsedUp-u.UsedDown)
		}
		for _, id := range domain.DecodeInbounds(u.Inbounds) {
			if n, ok := inboundName[id]; ok {
				p.Inbounds = append(p.Inbounds, n)
			}
		}
		out = append(out, p)
	}
	return epoch, out, slotUser, nil
}

// pullCounters applies one batch of traffic deltas. The batch position is stored in
// the same transaction, so a lost ack only causes a harmless re-delivery.
func (s *Syncer) pullCounters(ctx context.Context) {
	c, err := s.node.Counters(ctx)
	if err != nil {
		return
	}
	online := c.Online
	if online == nil {
		online = map[string]nodeapi.Online{}
	}
	s.online.Store(&online)

	epoch, seq, err := s.countersPos(ctx)
	if err != nil {
		s.log.Error("counters position", "err", err)
		return
	}
	if c.Epoch == epoch && c.Seq <= seq {
		_ = s.node.Ack(ctx, c.Epoch, c.Seq)
		return
	}
	now := s.now()
	hour, day := now.Unix()/3600, now.Unix()/86400
	err = s.st.Tx(ctx, func(q *db.Queries) error {
		rows, err := q.ListSlotUsers(ctx)
		if err != nil {
			return err
		}
		owner := make(map[string]int64, len(rows))
		for _, r := range rows {
			owner[r.SlotName] = r.UserID
		}
		for slot, t := range c.Slots {
			uid, ok := owner[slot]
			if !ok {
				continue
			}
			if err := q.AddUserTraffic(ctx, db.AddUserTrafficParams{Up: t.Up, Down: t.Down, ID: uid}); err != nil {
				return err
			}
			if err := q.AddTrafficHourly(ctx, db.AddTrafficHourlyParams{UserID: uid, Hour: hour, Up: t.Up, Down: t.Down}); err != nil {
				return err
			}
			if err := q.AddTrafficDaily(ctx, db.AddTrafficDailyParams{UserID: uid, Day: day, Up: t.Up, Down: t.Down}); err != nil {
				return err
			}
		}
		if err := q.SetNodeState(ctx, db.SetNodeStateParams{Key: "counters_epoch", Value: c.Epoch}); err != nil {
			return err
		}
		return q.SetNodeState(ctx, db.SetNodeStateParams{Key: "counters_seq", Value: strconv.FormatInt(c.Seq, 10)})
	})
	if err != nil {
		s.log.Error("store counters", "err", err)
		return
	}
	if err := s.node.Ack(ctx, c.Epoch, c.Seq); err != nil {
		s.log.Warn("ack counters", "err", err)
	}
	if c.Epoch != epoch {
		// The node started a new counter epoch (fresh volume): re-base its quotas.
		s.pushPolicies(ctx, true)
	}
}

func (s *Syncer) countersPos(ctx context.Context) (string, int64, error) {
	epoch, err := s.st.Q.GetNodeState(ctx, "counters_epoch")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	raw, err := s.st.Q.GetNodeState(ctx, "counters_seq")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	seq, _ := strconv.ParseInt(raw, 10, 64)
	return epoch, seq, nil
}

func (s *Syncer) nextRevision(ctx context.Context) (int64, error) {
	raw, err := s.st.Q.GetNodeState(ctx, "revision")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	rev, _ := strconv.ParseInt(raw, 10, 64)
	rev++
	return rev, s.st.Q.SetNodeState(ctx, db.SetNodeStateParams{Key: "revision", Value: strconv.FormatInt(rev, 10)})
}

func (s *Syncer) refreshHealth(ctx context.Context) {
	h, err := s.node.Health(ctx)
	view := &HealthView{CheckedAt: s.now()}
	if err != nil {
		view.Error = err.Error()
		s.health.Store(view)
		return
	}
	view.OK, view.Health, view.Listeners = true, h, h.Listeners
	s.health.Store(view)
	s.mu.Lock()
	applied := s.lastApplied.Revision
	s.mu.Unlock()
	// A node that lost its state (new volume, crash before saving) reports an older revision.
	if h.Revision < applied || (applied == 0 && h.Revision == 0) {
		s.mu.Lock()
		s.stateKey = ""
		s.mu.Unlock()
		signal(s.stateDirty)
	}
}

func (s *Syncer) maintain(ctx context.Context) {
	now := s.now()
	if err := s.resetPeriods(ctx, now); err != nil {
		s.log.Error("period resets", "err", err)
	}
	s.maintainPool(ctx, now)
	if err := s.st.Q.PruneTrafficHourly(ctx, now.Add(-62*24*time.Hour).Unix()/3600); err != nil {
		s.log.Error("prune traffic", "err", err)
	}
	if err := s.recordDevices(ctx, now); err != nil {
		s.log.Error("record devices", "err", err)
	}
	// Expiry is time-driven: the policy key changes when a user crosses expires_at.
	s.pushPolicies(ctx, false)
}

func (s *Syncer) resetPeriods(ctx context.Context, now time.Time) error {
	users, err := s.st.Q.ListUsers(ctx)
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
		if err := s.st.Q.ResetUserTraffic(ctx, db.ResetUserTrafficParams{PeriodStart: start, UpdatedAt: now.Unix(), ID: u.ID}); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		s.pushPolicies(ctx, true)
	}
	return nil
}

func (s *Syncer) maintainPool(ctx context.Context, now time.Time) {
	stats, err := s.pool.Stats(ctx)
	if err != nil {
		s.log.Error("pool stats", "err", err)
		return
	}
	quietHour, _, err := settings.Get[int](ctx, s.set, "quiet_hour_utc")
	if err != nil {
		s.log.Error("quiet hour", "err", err)
	}
	inQuietHour := now.UTC().Hour() == quietHour && now.Sub(s.lastPurge) > 20*time.Hour
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
		if err := s.pool.PurgeBurned(ctx); err != nil {
			s.log.Error("purge slots", "err", err)
			return
		}
		s.lastPurge = now
	}
	if refill {
		if err := s.pool.Refill(ctx, domain.RefillBatch); err != nil {
			s.log.Error("refill slots", "err", err)
			return
		}
	}
	s.log.Info("slot pool maintained", "free", stats.Free, "refill", refill, "purge", purge)
	s.applyState(ctx)
}

func (s *Syncer) recordDevices(ctx context.Context, now time.Time) error {
	online := s.Online()
	if len(online) == 0 {
		return nil
	}
	rows, err := s.st.Q.ListSlotUsers(ctx)
	if err != nil {
		return err
	}
	owner := make(map[string]int64, len(rows))
	for _, r := range rows {
		owner[r.SlotName] = r.UserID
	}
	return s.st.Tx(ctx, func(q *db.Queries) error {
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

func stateKey(st nodeapi.DesiredState) string {
	raw, _ := json.Marshal(struct {
		I []nodeapi.Inbound
		S []nodeapi.Slot
		T *nodeapi.TLSFiles
	}{st.Inbounds, st.Slots, st.TLS})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// policyKey ignores QuotaRemaining/BaseSeq: they change with every byte and the node
// tracks consumption itself between pushes.
func policyKey(ps []nodeapi.Policy) string {
	h := sha256.New()
	for _, p := range ps {
		raw, _ := json.Marshal([]any{p.Slot, p.Allowed, p.Inbounds, p.DeviceLimit, p.QuotaRemaining < 0})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}
