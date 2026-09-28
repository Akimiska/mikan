package nodesync

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
)

type fakeNode struct {
	batch    nodeapi.Counters
	acked    []int64
	applied  []nodeapi.DesiredState
	policies [][]nodeapi.Policy
}

func (f *fakeNode) Apply(_ context.Context, s nodeapi.DesiredState) (nodeapi.ApplyResult, error) {
	f.applied = append(f.applied, s)
	return nodeapi.ApplyResult{Revision: s.Revision}, nil
}
func (f *fakeNode) SetPolicies(_ context.Context, _ string, p []nodeapi.Policy) error {
	f.policies = append(f.policies, p)
	return nil
}
func (f *fakeNode) Counters(context.Context) (nodeapi.Counters, error) { return f.batch, nil }
func (f *fakeNode) Ack(_ context.Context, _ string, seq int64) error {
	f.acked = append(f.acked, seq)
	return nil
}
func (f *fakeNode) Health(context.Context) (nodeapi.Health, error) { return nodeapi.Health{}, nil }

func setup(t *testing.T) (*Syncer, *fakeNode, *store.Store, *domain.Users, *time.Time) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now }
	node := &fakeNode{}
	pool := domain.NewPool(st, clock)
	s := New(st, settings.New(st.Q), pool, node, func() (*nodeapi.TLSFiles, error) { return &nodeapi.TLSFiles{CertPEM: "c", KeyPEM: "k"}, nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)), clock)
	return s, node, st, domain.NewUsers(st, pool, s, clock), &now
}

func TestCountersAppliedOnce(t *testing.T) {
	s, node, st, users, _ := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	u, err := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	slot, _ := st.Q.GetSlot(ctx, u.SlotID.Int64)
	node.batch = nodeapi.Counters{Epoch: "e1", Seq: 1, Slots: map[string]nodeapi.Traffic{slot.Name: {Up: 100, Down: 900}, "s999999": {Up: 5}}}
	s.pullCounters(ctx)
	s.pullCounters(ctx) // ack lost → the node re-delivers the same batch
	got, _ := st.Q.GetUser(ctx, u.ID)
	if got.UsedUp != 100 || got.UsedDown != 900 || got.TotalDown != 900 {
		t.Fatalf("traffic applied twice or lost: up %d down %d", got.UsedUp, got.UsedDown)
	}
	if len(node.acked) != 2 || node.acked[1] != 1 {
		t.Fatalf("acks = %v, want the duplicate acked too", node.acked)
	}
	node.batch = nodeapi.Counters{Epoch: "e2", Seq: 1, Slots: map[string]nodeapi.Traffic{slot.Name: {Down: 50}}}
	s.pullCounters(ctx)
	got, _ = st.Q.GetUser(ctx, u.ID)
	if got.UsedDown != 950 {
		t.Fatalf("new epoch with seq 1 must be applied, down = %d", got.UsedDown)
	}
	if len(node.policies) == 0 {
		t.Fatal("epoch change must re-push policies")
	}
}

func TestPolicies(t *testing.T) {
	s, _, st, users, now := setup(t)
	ctx := context.Background()
	tariffs, _ := st.Q.ListTariffs(ctx)
	quota, _ := users.Create(ctx, domain.CreateInput{Name: "quota", TariffID: tariffs[1].ID})
	unlimited, _ := users.Create(ctx, domain.CreateInput{Name: "unl", TariffID: tariffs[2].ID})
	off, _ := users.Create(ctx, domain.CreateInput{Name: "off", TariffID: tariffs[2].ID})
	yes := true
	if _, err := users.Update(ctx, off.ID, domain.Patch{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := st.Q.AddUserTraffic(ctx, dbTraffic(quota.ID, 1<<30)); err != nil {
		t.Fatal(err)
	}
	_, ps, owners, err := s.policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[int64]nodeapi.Policy{}
	for _, p := range ps {
		by[owners[p.Slot]] = p
	}
	if p := by[quota.ID]; !p.Allowed || p.QuotaRemaining != 149<<30 || p.DeviceLimit != 3 {
		t.Fatalf("quota user policy %+v", p)
	}
	if p := by[unlimited.ID]; !p.Allowed || p.QuotaRemaining != -1 {
		t.Fatalf("unlimited policy %+v", p)
	}
	if by[off.ID].Allowed {
		t.Fatal("disabled user allowed")
	}
	*now = now.Add(31 * 24 * time.Hour)
	_, ps, owners, _ = s.policies(ctx)
	for _, p := range ps {
		if p.Allowed {
			t.Fatalf("expired user %d still allowed", owners[p.Slot])
		}
	}
}

// The server CLI writes inbounds straight to the database; the running panel must push
// them to the node without a restart.
func TestMaintainAppliesChangesMadeOutsideTheAPI(t *testing.T) {
	s, node, st, _, _ := setup(t)
	ctx := context.Background()
	s.applyState(ctx)
	s.maintain(ctx)
	base := len(node.applied)
	s.maintain(ctx)
	if len(node.applied) != base {
		t.Fatalf("an unchanged state was applied again: %d → %d", base, len(node.applied))
	}
	if _, err := domain.AddPreset(ctx, st, settings.New(st.Q), "trojan_reality", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	s.maintain(ctx)
	if len(node.applied) != base+1 {
		t.Fatalf("the new inbound was not applied: %d applies", len(node.applied))
	}
	var ports []string
	for _, in := range node.applied[len(node.applied)-1].Inbounds {
		ports = append(ports, in.Port)
	}
	if !slices.Contains(ports, "2087") {
		t.Fatalf("applied inbounds: %v", ports)
	}
}
