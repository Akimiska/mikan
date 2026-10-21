package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// A tariff's servers over HTTP: every server until chosen, a choice of nothing refused,
// and the subscription of a client of the tariff holds only the chosen servers. A balance
// group places the client on a node the tariff includes, not on one it leaves out.
func TestTariffServersOverHTTP(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355} {
		if err := settings.Set(ctx, settings.New(h.st.Q), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1"
	id := func(n int64) string { return strconv.FormatInt(n, 10) }
	q := h.st.Q

	ins, _ := q.ListInbounds(ctx)
	local := ins[0]
	b, err := q.CreateNode(ctx, db.CreateNodeParams{Name: "B", Address: "203.0.113.20:25305", PublicHost: "203.0.113.20", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	onB, err := q.CreateInbound(ctx, db.CreateInboundParams{NodeID: b.ID, Name: "b-" + local.Name, Preset: local.Preset, Port: local.Port, Config: local.Config, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}

	tariffs, _ := q.ListTariffs(ctx)
	std := tariffs[1]
	put := func(servers map[string]any) (*http.Response, []byte) {
		return h.do(http.MethodPut, api+"/tariffs/"+id(std.ID), map[string]any{"name": std.Name, "duration_days": 30, "reset_strategy": "period", "servers": servers}, csrf)
	}
	resp, body := h.do(http.MethodGet, api+"/tariffs", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"servers":{"all":true,"nodes":[],"inbounds":[]}`) {
		t.Fatalf("every server until chosen: %d %s", resp.StatusCode, body)
	}
	if resp, body := put(map[string]any{"all": false}); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "servers_empty") {
		t.Fatalf("a choice of nothing: %d %s", resp.StatusCode, body)
	}

	clock := func() time.Time { return h.now }
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: std.ID})
	if err != nil {
		t.Fatal(err)
	}
	proxies := func() []string {
		t.Helper()
		resp, body := h.do(http.MethodGet, "/"+subPath+"/"+u.SubToken, nil, map[string]string{"User-Agent": "mihomo/1.19.31"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("subscription: %d", resp.StatusCode)
		}
		var prof struct {
			Proxies []struct {
				Server string `json:"server"`
			} `json:"proxies"`
		}
		_ = unmarshalProfile(body, &prof)
		var out []string
		for _, p := range prof.Proxies {
			out = append(out, p.Server)
		}
		return out
	}
	on := func(host string, list []string) int {
		n := 0
		for _, s := range list {
			if s == host {
				n++
			}
		}
		return n
	}
	all := proxies()
	if on("203.0.113.20", all) != 1 || len(all) < 2 {
		t.Fatalf("every server: %v", all)
	}

	resp, body = put(map[string]any{"all": false, "inbounds": []int64{onB.ID}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"servers":{"all":false,"nodes":[],"inbounds":[`+id(onB.ID)+`]}`) {
		t.Fatalf("one protocol chosen: %d %s", resp.StatusCode, body)
	}
	if got := proxies(); len(got) != 1 || got[0] != "203.0.113.20" {
		t.Fatalf("only the chosen protocol: %v", got)
	}

	// Both nodes in one balance group: the client lands on the one the tariff includes.
	if _, err := h.st.DB.ExecContext(ctx, "UPDATE nodes SET balance_group = 'eu'"); err != nil {
		t.Fatal(err)
	}
	if resp, body := put(map[string]any{"all": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("every server again: %d %s", resp.StatusCode, body)
	}
	if got := proxies(); on("203.0.113.20", got) != 0 {
		t.Fatalf("the group places the client on the first node: %v", got)
	}
	resp, body = put(map[string]any{"all": false, "nodes": []int64{b.ID}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("node B whole: %d %s", resp.StatusCode, body)
	}
	if got := proxies(); len(got) != 1 || got[0] != "203.0.113.20" {
		t.Fatalf("placed again on the node the tariff includes: %v", got)
	}
	var tv []struct {
		ID      int64 `json:"id"`
		Servers struct {
			Nodes []int64 `json:"nodes"`
		} `json:"servers"`
	}
	resp, body = h.do(http.MethodGet, api+"/tariffs", nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &tv) != nil {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	for _, x := range tv {
		if x.ID == std.ID && (len(x.Servers.Nodes) != 1 || x.Servers.Nodes[0] != b.ID) {
			t.Fatalf("the view: %+v", x)
		}
	}
}
