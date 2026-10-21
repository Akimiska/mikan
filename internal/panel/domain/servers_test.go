package domain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"mikan/internal/panel/store/db"
)

// A tariff includes every server until servers are chosen: then only its chosen nodes,
// whole (a protocol added to one later is in too), and protocols. A client's own protocols
// stay over the tariff's. A choice of nothing or of what does not exist is refused whole,
// every server again clears the choice, and a node deleted leaves it.
func TestTariffServers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	q := st.Q
	tariffs, _ := q.ListTariffs(ctx)
	tr, other := tariffs[1], tariffs[0]
	id := sql.NullInt64{Int64: tr.ID, Valid: true}
	node := func(name string) db.Node {
		t.Helper()
		n, err := q.CreateNode(ctx, db.CreateNodeParams{Name: name, Address: name + ":25305", PublicHost: "203.0.113.1", CreatedAt: 1, UpdatedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	port := 20000
	inbound := func(n db.Node, name string) db.Inbound {
		t.Helper()
		port++
		in, err := q.CreateInbound(ctx, db.CreateInboundParams{NodeID: n.ID, Name: name, Preset: "vless_reality_xhttp", Port: fmt.Sprint(port), Config: "{}", CreatedAt: 1, UpdatedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	servers := func() Servers {
		t.Helper()
		s, err := TariffServers(ctx, q, id)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := node("A"), node("B")
	a1, b1, b2 := inbound(a, "a1"), inbound(b, "b1"), inbound(b, "b2")
	if s := servers(); !s.All || !s.Has(b2) {
		t.Fatalf("a tariff includes every server until servers are chosen: %+v", s)
	}

	if err := st.Tx(ctx, func(q *db.Queries) error {
		return SetTariffServers(ctx, q, tr.ID, ServersInput{Nodes: []int64{a.ID}, Inbounds: []int64{b1.ID}})
	}); err != nil {
		t.Fatal(err)
	}
	if s := servers(); s.All || !s.Has(a1) || !s.Has(b1) || s.Has(b2) {
		t.Fatalf("chosen: node A whole and b1: %+v", s)
	}
	a2 := inbound(a, "a2")
	if !servers().Has(a2) {
		t.Fatal("a protocol added to a chosen node is in the tariff too")
	}
	all, err := AllTariffServers(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || !ServersOf(all, sql.NullInt64{Int64: other.ID, Valid: true}).All || ServersOf(all, id).Has(b2) || !ServersOf(all, sql.NullInt64{}).All {
		t.Fatalf("every tariff at once: %+v", all)
	}

	u := db.User{TariffID: id}
	acc := AccessOf(u, servers())
	if acc.Everything() || !acc.Allows(a1) || acc.Allows(b2) {
		t.Fatal("a client without protocols of its own gets the tariff's servers")
	}
	if got := acc.IDs([]db.Inbound{a1, b1, b2}); !slices.Equal(got, []int64{a1.ID, b1.ID}) {
		t.Fatalf("ids: %v", got)
	}
	u.Inbounds = sql.NullString{String: fmt.Sprintf("[%d]", b2.ID), Valid: true}
	if acc := AccessOf(u, servers()); !acc.Allows(b2) || acc.Allows(a1) {
		t.Fatal("the card's own protocols stay over the tariff's servers")
	}
	if !AccessOf(db.User{}, AllServers).Everything() || AccessOf(db.User{}, AllServers).IDs([]db.Inbound{a1}) != nil {
		t.Fatal("every server is no list")
	}

	refused := func(in ServersInput, code string) {
		t.Helper()
		err := st.Tx(ctx, func(q *db.Queries) error { return SetTariffServers(ctx, q, tr.ID, in) })
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Code != code {
			t.Fatalf("%+v: %v, want %s", in, err, code)
		}
		if s := servers(); s.All || !s.Has(b1) || !s.Has(a2) {
			t.Fatalf("a refused choice leaves the tariff's as it was: %+v", s)
		}
	}
	refused(ServersInput{}, "servers_empty")
	refused(ServersInput{Nodes: []int64{a.ID, 999_999}}, "node_not_found")
	refused(ServersInput{Inbounds: []int64{999_999}}, "inbound_not_found")

	if _, err := st.DB.ExecContext(ctx, "DELETE FROM nodes WHERE id = $1", a.ID); err != nil {
		t.Fatal(err)
	}
	if s := servers(); s.Nodes[a.ID] || !s.Has(b1) {
		t.Fatalf("a deleted node leaves the tariff: %+v", s)
	}
	if err := st.Tx(ctx, func(q *db.Queries) error { return SetTariffServers(ctx, q, tr.ID, ServersInput{All: true}) }); err != nil {
		t.Fatal(err)
	}
	if s := servers(); !s.All {
		t.Fatal("every server again")
	}
	if rows, _ := q.ListTariffInbounds(ctx, tr.ID); len(rows) != 0 {
		t.Fatalf("every server keeps no list: %v", rows)
	}
}
