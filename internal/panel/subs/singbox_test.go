package subs

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type sbConfig struct {
	DNS struct {
		Servers []map[string]any `json:"servers"`
		Rules   []map[string]any `json:"rules"`
		Final   string           `json:"final"`
	} `json:"dns"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Route     struct {
		Rules    []map[string]any `json:"rules"`
		RuleSet  []map[string]any `json:"rule_set"`
		Final    string           `json:"final"`
		Resolver string           `json:"default_domain_resolver"`
	} `json:"route"`
}

// lists stands in for GitHub: the panel's Clash lists, as small as they can be.
func lists(t *testing.T) {
	t.Helper()
	old := singboxList
	singboxList = func(_ context.Context, src string) ([]string, error) {
		switch {
		case strings.HasSuffix(src, "process-direct-apk.yaml"):
			return []string{"PROCESS-NAME,ru.sberbankmobile", "PROCESS-NAME,ru.gosuslugi.app"}, nil
		case strings.HasSuffix(src, "process-direct-pc.yaml"):
			return []string{"PROCESS-NAME,qbittorrent.exe"}, nil
		case strings.HasSuffix(src, "domain-direct-suffix.yaml"):
			return []string{"DOMAIN-SUFFIX,gosuslugi.ru", "DOMAIN-SUFFIX,sberbank.ru"}, nil
		case strings.HasSuffix(src, "clV2_provider.yaml"):
			return []string{"DOMAIN-SUFFIX,instagram.com", "IP-CIDR,149.154.160.0/20,no-resolve"}, nil
		}
		return []string{"DOMAIN-KEYWORD,example"}, nil
	}
	t.Cleanup(func() { singboxList = old })
}

func renderSingBox(t *testing.T, prof Profile, r Routing, src string) sbConfig {
	t.Helper()
	raw, err := SingBox(context.Background(), prof, Groups{}, r, src)
	if err != nil {
		t.Fatal(err)
	}
	var c sbConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	return c
}

func outbound(c sbConfig, tag string) map[string]any {
	for _, o := range c.Outbounds {
		if o["tag"] == tag {
			return o
		}
	}
	return nil
}

// The Clash profile as a sing-box config: the proxies sing-box can carry, the groups over
// them, the rules with their lists, DNS, and a TUN of its own for the official apps.
func TestSingBox(t *testing.T) {
	lists(t)
	prof := twoNodes(t)
	prof.Direct = []string{"203.0.113.7"}
	prof.Rules = []string{"DOMAIN-SUFFIX,sber.ru,DIRECT", "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT"}
	prof.Routes = Routes{Services: map[string]string{"youtube": TargetVPN, "ads": TargetBlock}, Direct: []string{"ru"}}
	c := renderSingBox(t, prof, RoutingRUDirect, "")

	if len(c.Inbounds) != 1 || c.Inbounds[0]["type"] != "tun" {
		t.Fatalf("inbounds: %v", c.Inbounds)
	}
	var types []string
	for _, o := range c.Outbounds {
		types = append(types, o["type"].(string))
		if strings.Contains(o["tag"].(string), "XHTTP") {
			t.Errorf("sing-box has no XHTTP: %v", o)
		}
	}
	for _, want := range []string{"selector", "urltest", "vless", "hysteria2", "tuic", "direct"} {
		if !slices.Contains(types, want) {
			t.Errorf("no %s outbound: %v", want, types)
		}
	}
	vision := outbound(c, "🇳🇱 VLESS Vision")
	tls, _ := vision["tls"].(map[string]any)
	if vision["flow"] != "xtls-rprx-vision" || tls["reality"] == nil || tls["utls"] == nil {
		t.Fatalf("REALITY needs uTLS in sing-box: %v", vision)
	}
	if main := outbound(c, "VPN"); main == nil || c.Route.Final != "VPN" {
		t.Fatalf("main group and final: %v %q", main, c.Route.Final)
	}

	rules, _ := json.Marshal(c.Route.Rules)
	for _, want := range []string{`"action":"sniff"`, `"action":"hijack-dns"`, `"domain_suffix":["sber.ru"],"outbound":"direct"`,
		`"ip_is_private":true,"outbound":"direct"`, `"action":"reject","rule_set":["geosite-category-ads-all"]`, `"outbound":"VPN","rule_set":["geosite-youtube"]`,
		`"mode":"and"`, `"rule_set":["list-mikan-ru-apps"]`, `"outbound":"direct","rule_set":["geosite-category-ru"]`} {
		if !strings.Contains(string(rules), want) {
			t.Errorf("rules lack %s:\n%s", want, rules)
		}
	}
	sets := map[string]map[string]any{}
	for _, s := range c.Route.RuleSet {
		sets[s["tag"].(string)] = s
	}
	if yt := sets["geosite-youtube"]; yt["type"] != "remote" || yt["download_detour"] != "VPN" || !strings.HasSuffix(yt["url"].(string), "/sing/geo/geosite/youtube.srs") {
		t.Errorf("remote set: %v", yt)
	}
	apps, _ := json.Marshal(sets["list-mikan-ru-apps"])
	if !strings.Contains(string(apps), `"package_name":["ru.sberbankmobile","ru.gosuslugi.app"]`) {
		t.Errorf("Android packages: %s", apps)
	}
	pc, _ := json.Marshal(sets["list-mikan-ru-pc"])
	if !strings.Contains(string(pc), `"process_name":["qbittorrent.exe"]`) {
		t.Errorf("programs: %s", pc)
	}
	if sets["list-mikan-ru-apps-wildcard"] != nil {
		t.Error("a list of a younger mihomo type went in")
	}
	if c.DNS.Final != "remote" || c.Route.Resolver != "local" || len(c.DNS.Servers) < 2 {
		t.Fatalf("dns: %+v %q", c.DNS, c.Route.Resolver)
	}
}

// A pinned self-signed certificate is left out: sing-box 1.12 cannot pin it.
func TestSingBoxLeavesPinnedOut(t *testing.T) {
	lists(t)
	c := renderSingBox(t, profile(t, "ab12"), RoutingAll, "")
	for _, o := range c.Outbounds {
		if o["type"] == "hysteria2" || o["type"] == "tuic" {
			t.Errorf("a pinned proxy went in: %v", o)
		}
	}
	if outbound(c, "VLESS Vision") == nil {
		t.Error("REALITY needs no pin")
	}
}

// The blocked mode and an own profile come over too.
func TestSingBoxModes(t *testing.T) {
	lists(t)
	prof := twoNodes(t)
	c := renderSingBox(t, prof, RoutingBlocked, "")
	rules, _ := json.Marshal(c.Route.Rules)
	if c.Route.Final != "direct" || !strings.Contains(string(rules), `"outbound":"VPN","rule_set":["list-mikan-blocked"]`) {
		t.Fatalf("blocked: %q %s", c.Route.Final, rules)
	}
	own := renderSingBox(t, prof, RoutingAll, ownTemplate)
	if outbound(own, "🌍 Сеть") == nil || own.Route.Final != "🌍 Сеть" || outbound(own, "🇯🇵 Япония") != nil {
		t.Fatalf("own profile: %+v", own.Outbounds)
	}
	if net := outbound(own, "🌍 Сеть"); slices.Contains(net["outbounds"].([]any), any("🇯🇵 Япония")) {
		t.Fatalf("a closed group stays a member: %v", net)
	}
}
