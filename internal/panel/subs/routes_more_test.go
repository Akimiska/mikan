package subs

import (
	"slices"
	"strings"
	"testing"
)

// Lists of the admin's own go where they are sent, through the tunnel to download; one in
// .mrs only to an app whose core reads it.
func TestOwnLists(t *testing.T) {
	prof := twoNodes(t)
	prof.Routes = Routes{Lists: []RouteList{
		{Name: "wl", URL: "https://example.com/wl.yaml", Behavior: "classical", Format: "yaml", Target: TargetVPN},
		{Name: "ru-ip", URL: "https://example.com/ru.txt", Behavior: "ipcidr", Format: "text", Target: TargetDirect},
		{Name: "games", URL: "https://example.com/games.yaml", Behavior: "domain", Format: "yaml", Target: NodeTarget(2)},
		{Name: "ads", URL: "https://example.com/ads.mrs", Behavior: "domain", Format: "mrs", Target: TargetBlock},
	}}
	cfg, raw := renderRouted(t, prof, RoutingAll)
	for _, want := range []string{"RULE-SET,own-wl,VPN", "RULE-SET,own-ru-ip,DIRECT,no-resolve", "RULE-SET,own-games,📋 games"} {
		if !slices.Contains(cfg.Rules, want) {
			t.Errorf("no %s:\n%s", want, strings.Join(cfg.Rules, "\n"))
		}
	}
	if p := cfg.Providers["own-wl"]; p == nil || p["proxy"] != "VPN" || p["url"] != "https://example.com/wl.yaml" {
		t.Errorf("provider: %v", cfg.Providers)
	}
	if cfg.Providers["own-ads"] != nil || strings.Contains(string(raw), "own-ads") {
		t.Error("an .mrs list went to a core that may not read it")
	}
	prof.App = App{Family: FamilyMihomo, Core: Version{1, 19, 32}}
	if cfg, _ := renderRouted(t, prof, RoutingAll); !slices.Contains(cfg.Rules, "RULE-SET,own-ads,REJECT") {
		t.Errorf("a new core takes .mrs: %v", cfg.Rules)
	}
}

// How servers are picked: no group per country, the first that works, the interval.
func TestServerChoice(t *testing.T) {
	prof := twoNodes(t)
	prof.Routes.Servers = Servers{NoCountries: true, Auto: "fallback", Interval: 120}
	cfg, raw := renderRouted(t, prof, RoutingAll)
	if len(cfg.Groups) != 3 || strings.Contains(string(raw), "🇺🇸 США\n") {
		t.Fatalf("groups: %+v", cfg.Groups)
	}
	if !strings.Contains(string(raw), "type: fallback") || !strings.Contains(string(raw), "interval: 120") || strings.Contains(string(raw), "tolerance") {
		t.Fatalf("auto:\n%s", raw)
	}
}

// QUIC blocked ahead of the admin's rules, the sniffer, and DNS without fake addresses.
func TestTune(t *testing.T) {
	prof := profile(t, "")
	prof.Rules = []string{"DOMAIN-SUFFIX,sber.ru,DIRECT"}
	prof.Routes.Tune = Tune{BlockQUIC: true, Sniffer: true, RealIP: true}
	cfg, raw := renderRouted(t, prof, RoutingRUDirect)
	quic, own := slices.Index(cfg.Rules, "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT"), slices.Index(cfg.Rules, "DOMAIN-SUFFIX,sber.ru,DIRECT")
	if quic < 0 || own < quic {
		t.Fatalf("rules:\n%s", strings.Join(cfg.Rules, "\n"))
	}
	if cfg.DNS["enhanced-mode"] != "redir-host" || cfg.DNS["fake-ip-range"] != nil || !strings.Contains(string(raw), "sniffer:") {
		t.Fatalf("dns %v\n%s", cfg.DNS, raw)
	}
	if _, raw := renderRouted(t, profile(t, ""), RoutingRUDirect); strings.Contains(string(raw), "sniffer") || strings.Contains(string(raw), "DST-PORT") {
		t.Fatal("left alone, the profile is as it was")
	}
}

func TestOwnListsCheck(t *testing.T) {
	exists := func(id int64) bool { return id == 2 }
	ok := RouteList{Name: "wl", URL: "https://example.com/wl.yaml", Behavior: "classical", Format: "yaml", Target: TargetVPN}
	if err := (Routes{Lists: []RouteList{ok}}).Check(exists); err != nil {
		t.Fatal(err)
	}
	with := func(f func(*RouteList)) Routes {
		l := ok
		f(&l)
		return Routes{Lists: []RouteList{l}}
	}
	for code, r := range map[string]Routes{
		"routes_list_name":     with(func(l *RouteList) { l.Name = "Bad Name" }),
		"routes_list_url":      with(func(l *RouteList) { l.URL = "http://example.com/x" }),
		"routes_list_behavior": with(func(l *RouteList) { l.Behavior = "nope" }),
		"routes_list_format":   with(func(l *RouteList) { l.Format = "mrs" }), // classical has no .mrs
		"routes_target":        with(func(l *RouteList) { l.Target = NodeTarget(9) }),
		"routes_servers":       {Servers: Servers{Interval: 5}},
	} {
		if err := r.Check(exists); err == nil || err.Error() != code {
			t.Errorf("%s: %v", code, err)
		}
	}
	if err := (Routes{Lists: []RouteList{ok, ok}}).Check(exists); err == nil || err.Error() != "routes_list_name" {
		t.Errorf("a name twice: %v", err)
	}
}
