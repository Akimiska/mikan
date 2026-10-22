package subs

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

const gib = int64(1) << 30

func ptr(n int64) *int64 { return &n }

// What the Mikan app gets of each group: the traffic of a group of its own apart from the
// main traffic, the names of its servers in the profile, nothing of a group the client
// cannot reach or that the plan leaves out.
func TestAppGroupsOf(t *testing.T) {
	pools := []db.TrafficPool{{ID: 1, Name: "Белые списки"}, {ID: 2, Name: "Игры"}, {ID: 3, Name: "Премиум +"}, {ID: 4, Name: "Закрытая"},
		{ID: 5, Name: "Европа"}, {ID: 6, Name: "Не для него"}, {ID: 7, Name: "Без лимита в тарифе"}, {ID: 8, Name: "Пустая"}}
	rows := []db.UserPool{
		{PoolID: 1, Mode: domain.PoolUnlimited, UsedDown: gib},
		{PoolID: 2, Mode: domain.PoolOwn, TrafficLimit: sql.NullInt64{Int64: 5 * gib, Valid: true}, UsedDown: 5 * gib},
		{PoolID: 3, Mode: domain.PoolOwn, TrafficLimit: sql.NullInt64{Int64: 15 * gib, Valid: true}, UsedUp: gib, UsedDown: 2 * gib},
		{PoolID: 4, Mode: domain.PoolClosed},
		{PoolID: 6, Mode: domain.PoolOwn, TrafficLimit: sql.NullInt64{Int64: gib, Valid: true}},
		{PoolID: 7, Mode: domain.PoolOwn, UsedDown: 10},
		{PoolID: 8, Mode: domain.PoolUnlimited},
	}
	left := domain.GrantsLeft{{9, 3}: 2 * gib, {9, 0}: 50 * gib}
	reach := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: true, 7: true, 8: true}
	ps := []proxy{{name: "🇳🇱 VLESS"}, {name: "🇷🇺 VLESS", pool: 1}, {name: "🇺🇸 VLESS Reality", pool: 3}, {name: "🇺🇸 Hysteria2", pool: 3}, {name: "🇩🇪 Trojan", pool: 5}}

	got := appGroupsOf(pools, rows, left, 9, reach, ps)
	want := []AppGroup{
		{ID: 1, Name: "Белые списки", Mode: "unlimited", Used: gib, State: "active", Proxies: []string{"🇷🇺 VLESS"}},
		{ID: 2, Name: "Игры", Mode: "own", Used: 5 * gib, Limit: ptr(5 * gib), Left: ptr(0), State: "exhausted", Proxies: []string{}},
		{ID: 3, Name: "Премиум +", Mode: "own", Used: 3 * gib, Limit: ptr(15 * gib), Extra: 2 * gib, Left: ptr(14 * gib), State: "active",
			Proxies: []string{"🇺🇸 VLESS Reality", "🇺🇸 Hysteria2"}},
		{ID: 5, Name: "Европа", Mode: "main", State: "active", Proxies: []string{"🇩🇪 Trojan"}},
		// A limit of its own with no number is no limit: the app shows it as unlimited.
		{ID: 7, Name: "Без лимита в тарифе", Mode: "unlimited", Used: 10, State: "active", Proxies: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groups:\n got %s\nwant %s", js(t, got), js(t, want))
	}
}

func js(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeGroups(t *testing.T, v string) AppGroups {
	t.Helper()
	raw, ok := strings.CutPrefix(v, "base64:")
	if !ok {
		t.Fatalf("no base64: prefix: %q", v)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	var g AppGroups
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAppGroupsHeader(t *testing.T) {
	if v := (AppGroups{V: 1}).header(); v != "" {
		t.Fatalf("no groups, yet a header: %q", v)
	}
	g := AppGroups{V: 1, ResetsAt: 1793491200, Groups: []AppGroup{
		{ID: 3, Name: "Премиум +", Mode: "own", Used: 3 * gib, Limit: ptr(15 * gib), Left: ptr(12 * gib), State: "active", Proxies: []string{"🇺🇸 VLESS Reality"}},
	}}
	v := g.header()
	if back := decodeGroups(t, v); !reflect.DeepEqual(back, g) {
		t.Fatalf("round trip: %+v", back)
	}
	// The JSON the app reads, field by field: a renamed field breaks every app out there.
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "base64:"))
	if want := `{"v":1,"resets_at":1793491200,"groups":[{"id":3,"name":"Премиум +","mode":"own","used":3221225472,"limit":16106127360,"left":12884901888,"state":"active","proxies":["🇺🇸 VLESS Reality"]}]}`; string(raw) != want {
		t.Fatalf("JSON:\n got %s\nwant %s", raw, want)
	}
}

// A subscription with a great many servers in its groups still gets a header that fits
// behind a reverse proxy: the server names go first (and with them the groups that are only
// names), the traffic of the groups of their own stays.
func TestAppGroupsHeaderFits(t *testing.T) {
	g := AppGroups{V: 1}
	for i := range 6 {
		var names []string
		for j := range 20 {
			names = append(names, "🇺🇸 Сервер "+strconv.Itoa(i)+"-"+strconv.Itoa(j)+" VLESS Reality")
		}
		g.Groups = append(g.Groups, AppGroup{ID: int64(i + 1), Name: "Группа " + strconv.Itoa(i), Mode: "own", Used: gib, Limit: ptr(5 * gib), Left: ptr(4 * gib), State: "active", Proxies: names})
	}
	g.Groups = append(g.Groups, AppGroup{ID: 99, Name: "Только метка", Mode: "main", State: "active", Proxies: []string{"🇩🇪 Trojan"}})
	v := g.header()
	if len(v) > appGroupsMax {
		t.Fatalf("header of %d bytes, the most is %d", len(v), appGroupsMax)
	}
	back := decodeGroups(t, v)
	if len(back.Groups) != 6 {
		t.Fatalf("want the 6 groups of their own and no label-only group, got %d", len(back.Groups))
	}
	for _, x := range back.Groups {
		if x.Proxies != nil || x.Limit == nil || *x.Limit != 5*gib {
			t.Fatalf("names must be null (did not fit), the traffic kept: %+v", x)
		}
	}
	if len(g.Groups[0].Proxies) != 20 {
		t.Fatal("header() changed the groups it was given")
	}
}

func TestWantsFeature(t *testing.T) {
	for _, c := range []struct {
		values []string
		want   bool
	}{
		{nil, false},
		{[]string{"groups"}, true},
		{[]string{"Groups"}, true},
		{[]string{"news, groups"}, true},
		{[]string{"news", " groups "}, true},
		{[]string{"groupsx"}, false},
		{[]string{"news"}, false},
	} {
		h := http.Header{}
		for _, v := range c.values {
			h.Add(FeaturesHeader, v)
		}
		if got := wantsFeature(h, featureGroups); got != c.want {
			t.Errorf("%q: got %v, want %v", c.values, got, c.want)
		}
	}
}
