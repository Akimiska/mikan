package nodesync

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// A tariff's chosen servers reach the node at once: its clients get the tariff's inbounds
// here spelled out, a tariff with nothing here turns them away, a client's own protocols
// stay over the tariff's, and a tariff with every server keeps the list empty (all).
func TestTariffServersOnTheNode(t *testing.T) {
	s, _, st, users, _ := setup(t)
	ctx := context.Background()
	q := st.Q
	ins, _ := q.ListInbounds(ctx)
	if len(ins) < 2 {
		t.Fatalf("the seeded node needs two inbounds: %d", len(ins))
	}
	tariffs, _ := q.ListTariffs(ctx)
	cheap, rich := tariffs[1], tariffs[2]
	a, _ := users.Create(ctx, domain.CreateInput{Name: "a", TariffID: cheap.ID})
	b, _ := users.Create(ctx, domain.CreateInput{Name: "b", TariffID: rich.ID})
	choose := func(tariff int64, in domain.ServersInput) {
		t.Helper()
		if err := st.Tx(ctx, func(q *db.Queries) error { return domain.SetTariffServers(ctx, q, tariff, in) }); err != nil {
			t.Fatal(err)
		}
		s.m.PoliciesChanged() // what the API says after the tariff's servers change
	}
	policies := func() map[int64]nodeapi.Policy {
		t.Helper()
		_, ps, owners, err := s.policies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]nodeapi.Policy{}
		for _, p := range ps {
			out[owners[p.Slot]] = p
		}
		return out
	}

	choose(cheap.ID, domain.ServersInput{Inbounds: []int64{ins[1].ID}})
	ps := policies()
	if p := ps[a.ID]; !p.Allowed || !slices.Equal(p.Inbounds, []string{ins[1].Name}) {
		t.Fatalf("the tariff's one inbound here: %+v", p)
	}
	if p := ps[b.ID]; !p.Allowed || len(p.Inbounds) != 0 {
		t.Fatalf("a tariff with every server keeps the list empty: %+v", p)
	}

	choose(cheap.ID, domain.ServersInput{Nodes: []int64{s.id}})
	var all []string
	for _, in := range ins {
		if in.NodeID == s.id {
			all = append(all, in.Name)
		}
	}
	if p := policies()[a.ID]; !p.Allowed || len(p.Inbounds) != len(all) {
		t.Fatalf("the node whole: %+v, want %v", p, all)
	}

	other, err := q.CreateNode(ctx, db.CreateNodeParams{Name: "B", Address: "203.0.113.2:25305", PublicHost: "203.0.113.2", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	choose(cheap.ID, domain.ServersInput{Nodes: []int64{other.ID}})
	if p := policies()[a.ID]; p.Allowed {
		t.Fatalf("a tariff with nothing here: the node turns its clients away: %+v", p)
	}

	raw := "[" + strconv.FormatInt(ins[0].ID, 10) + "]"
	if _, err := st.DB.ExecContext(ctx, "UPDATE users SET inbounds = $1 WHERE id = $2", raw, a.ID); err != nil {
		t.Fatal(err)
	}
	s.m.PoliciesChanged()
	if p := policies()[a.ID]; !p.Allowed || !slices.Equal(p.Inbounds, []string{ins[0].Name}) {
		t.Fatalf("the client's own protocols stay over the tariff's: %+v", p)
	}
}
