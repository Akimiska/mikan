package app

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// groupWorld is a seeded panel with an admin signed in, for the server group endpoints.
func groupWorld(t *testing.T) (*harness, map[string]string) {
	t.Helper()
	h := newHarness(t)
	if err := domain.Seed(t.Context(), h.st, h.now); err != nil {
		t.Fatal(err)
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	return h, map[string]string{"X-CSRF-Token": h.csrf}
}

func poolURL(id int64, part string) string {
	return "/" + adminPath + "/api/v1/pools/" + strconv.FormatInt(id, 10) + part
}

// poolOf is the group inbound id is in now, 0 for the main servers.
func poolOf(t *testing.T, q *db.Queries, id int64) int64 {
	t.Helper()
	in, err := q.GetInbound(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return in.PoolID.Int64
}

// A group's inbounds are what the last list says: the ones listed come over from the group
// they were in, the ones it had and the list leaves out go back to the main servers, and an
// unknown inbound changes nothing.
func TestGroupInbounds(t *testing.T) {
	h, csrf := groupWorld(t)
	ctx := t.Context()
	q := h.st.Q
	a, _ := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "A", CreatedAt: 1})
	b, _ := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "B", CreatedAt: 1})
	ins, _ := q.ListInbounds(ctx)
	if len(ins) < 3 {
		t.Fatalf("the seed made %d inbounds", len(ins))
	}
	one, two, three := ins[0].ID, ins[1].ID, ins[2].ID
	put := func(pool int64, ids ...int64) (int, string) {
		t.Helper()
		if ids == nil {
			ids = []int64{} // an empty list, as the panel's page sends it, not null
		}
		resp, body := h.do(http.MethodPut, poolURL(pool, "/inbounds"), map[string]any{"inbound_ids": ids}, csrf)
		return resp.StatusCode, string(body)
	}
	if code, body := put(a.ID, one, two); code != http.StatusOK {
		t.Fatalf("A: %d %s", code, body)
	}
	if poolOf(t, q, one) != a.ID || poolOf(t, q, two) != a.ID || poolOf(t, q, three) != 0 {
		t.Fatal("A did not take its inbounds")
	}
	// B takes two from A and three from the main servers; A keeps one.
	if code, body := put(b.ID, two, three); code != http.StatusOK {
		t.Fatalf("B: %d %s", code, body)
	}
	if poolOf(t, q, one) != a.ID || poolOf(t, q, two) != b.ID || poolOf(t, q, three) != b.ID {
		t.Fatal("an inbound did not move from A to B, or A lost one it kept")
	}
	// An empty list sends A's inbounds back to the main servers, and leaves B's alone.
	if code, body := put(a.ID); code != http.StatusOK {
		t.Fatalf("A emptied: %d %s", code, body)
	}
	if poolOf(t, q, one) != 0 || poolOf(t, q, two) != b.ID || poolOf(t, q, three) != b.ID {
		t.Fatal("emptying A touched another group or kept its inbound")
	}
	if code, body := put(a.ID, one, 999999); code != http.StatusUnprocessableEntity || !strings.Contains(body, "inbound_not_found") {
		t.Fatalf("an unknown inbound: %d %s", code, body)
	}
	if poolOf(t, q, one) != 0 {
		t.Fatal("a refused list changed the groups")
	}
}

// How tariffs hold a group is set for each tariff listed, in one go: the tariff's other groups
// stay as they are, closing reaches the tariff's clients at once, and a list one tariff of
// which cannot take it (a gift that would add to no quota) changes no tariff.
func TestGroupTariffs(t *testing.T) {
	h, csrf := groupWorld(t)
	ctx := t.Context()
	q := h.st.Q
	g, _ := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "G", CreatedAt: 1})
	other, _ := q.CreateTrafficPool(ctx, db.CreateTrafficPoolParams{Name: "Other", CreatedAt: 1})
	ins, _ := q.ListInbounds(ctx)
	// The group serves: an inbound that is on is in it.
	if _, err := q.SetGroupInbounds(ctx, db.SetGroupInboundsParams{Ids: []int64{ins[0].ID}, PoolID: g.ID}); err != nil {
		t.Fatal(err)
	}
	tariffs, _ := q.ListTariffs(ctx)
	t1, t2 := tariffs[1], tariffs[2]
	gb := func(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n << 30, Valid: true} }
	if err := q.AddTariffPool(ctx, db.AddTariffPoolParams{TariffID: t1.ID, PoolID: other.ID, Mode: domain.PoolOwn, TrafficLimit: gb(10)}); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return h.now }
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "u", TariffID: t1.ID})
	if err != nil {
		t.Fatal(err)
	}
	put := func(rows ...map[string]any) (int, string) {
		t.Helper()
		resp, body := h.do(http.MethodPut, poolURL(g.ID, "/tariffs"), map[string]any{"tariffs": rows}, csrf)
		return resp.StatusCode, string(body)
	}
	mode := func(tariff, pool int64) (string, int64) {
		t.Helper()
		rows, _ := q.ListTariffPools(ctx, tariff)
		for _, r := range rows {
			if r.PoolID == pool {
				return r.Mode, r.TrafficLimit.Int64
			}
		}
		return domain.PoolMain, 0
	}
	if code, body := put(map[string]any{"tariff_id": t1.ID, "mode": "closed"}, map[string]any{"tariff_id": t2.ID, "mode": "own", "traffic_limit": 5 << 30}); code != http.StatusOK {
		t.Fatalf("set: %d %s", code, body)
	}
	if m, _ := mode(t1.ID, g.ID); m != domain.PoolClosed {
		t.Fatalf("t1 has G %s", m)
	}
	if m, limit := mode(t1.ID, other.ID); m != domain.PoolOwn || limit != 10<<30 {
		t.Fatalf("t1's other group is %s %d now", m, limit)
	}
	if m, limit := mode(t2.ID, g.ID); m != domain.PoolOwn || limit != 5<<30 {
		t.Fatalf("t2 has G %s %d", m, limit)
	}
	ups, _ := q.ListUserPools(ctx, u.ID)
	if m, _ := domain.UserPoolOf(ups, g.ID); m.Mode != domain.PoolClosed {
		t.Fatalf("the client of t1 has G %+v", ups)
	}

	if code, body := put(map[string]any{"tariff_id": t1.ID, "mode": "main"}, map[string]any{"tariff_id": t1.ID, "mode": "closed"}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "tariff_duplicate") {
		t.Fatalf("a tariff twice: %d %s", code, body)
	}
	if code, body := put(map[string]any{"tariff_id": t2.ID, "mode": "own"}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "pool_own_limit") {
		t.Fatalf("own without a limit: %d %s", code, body)
	}

	// t2 gives a package of G with every payment: G leaving its own limit on t2 is refused,
	// and t1, listed first, is not changed either.
	pkg, err := q.CreateTrafficPackage(ctx, db.CreateTrafficPackageParams{Name: "G 10", Bytes: 10 << 30, PoolID: sql.NullInt64{Int64: g.ID, Valid: true}, Lifetime: domain.LifetimeUsed, CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.AddTariffGift(ctx, db.AddTariffGiftParams{TariffID: t2.ID, PackageID: pkg.ID}); err != nil {
		t.Fatal(err)
	}
	if code, body := put(map[string]any{"tariff_id": t1.ID, "mode": "own", "traffic_limit": 1 << 30}, map[string]any{"tariff_id": t2.ID, "mode": "main"}); code != http.StatusUnprocessableEntity || !strings.Contains(body, "gift_not_fit") {
		t.Fatalf("a gift that would fit nothing: %d %s", code, body)
	}
	if m, _ := mode(t1.ID, g.ID); m != domain.PoolClosed {
		t.Fatalf("the refused list changed t1: G is %s", m)
	}
	if m, _ := mode(t2.ID, g.ID); m != domain.PoolOwn {
		t.Fatalf("the refused list changed t2: G is %s", m)
	}
}

// Applying a tariff to its subscriptions with no part of it chosen is refused: it would
// do nothing, and the admin meant something.
func TestApplyTariffNeedsAPart(t *testing.T) {
	h, csrf := groupWorld(t)
	tariffs, _ := h.st.Q.ListTariffs(t.Context())
	url := "/" + adminPath + "/api/v1/tariffs/" + strconv.FormatInt(tariffs[1].ID, 10) + "/apply"
	if resp, body := h.do(http.MethodPost, url, map[string]any{}, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "apply_nothing") {
		t.Fatalf("apply nothing: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodPost, url, map[string]any{"groups": true}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("apply the groups: %d %s", resp.StatusCode, body)
	}
}
