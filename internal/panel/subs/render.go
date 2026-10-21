// Package subs renders subscriptions: share links for Xray/sing-box based clients and a
// mihomo profile for Clash-family clients.
package subs

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

// Endpoint describes how clients reach one node.
type Endpoint struct {
	Host      string // IP or domain the client connects to
	SNI       string // TLS server name for Hysteria2/TUIC; empty for IP-only nodes
	PinSHA256 string // hex SHA-256 of the node's self-signed Hysteria2/TUIC certificate
	// SNIRouter: the node shares its 443 between the inbounds marked for it, which clients
	// reach on 443.
	SNIRouter bool
}

// Node is one server in the subscription.
type Node struct {
	ID int64
	// Name is the node's country group, e.g. "🇳🇱 Нидерланды"; with several nodes its
	// leading flag (or the whole name) prefixes proxy names.
	Name     string
	Endpoint Endpoint
}

// Groups are the proxy-group names Clash-family apps show; the admin can rename them.
type Groups struct {
	Main string // selector, "VPN" by default
	Auto string // url-test, "Авто" by default, "Auto" on a panel in English
	Icon string // AoiVPN fork: optional icon URL for the main group (mihomo `icon`)
}

// AliasGroup is the name Clash apps assume for the main group in the rules they inject
// themselves (Koala Clash per-app routing: "PROCESS-NAME,app.exe,PROXY").
const AliasGroup = "PROXY"

// Routing is how Clash-family apps split traffic between the tunnel and the direct path.
type Routing string

const (
	// RoutingRUDirect sends Russian sites and IPs past the tunnel by mihomo's own geodata
	// (MetaCubeX geosite category-ru and GeoIP ru): banks and state services refuse
	// foreign IPs, and the node does not carry traffic that needs no VPN.
	RoutingRUDirect Routing = "ru_direct"
	// RoutingAll sends everything but the LAN through the tunnel and needs no geodata.
	RoutingAll Routing = "all"
	// RoutingBlocked sends only what is blocked or slowed down in Russia through the
	// tunnel (blockedLists, and the services sent to it), the rest goes direct. Apps that
	// cannot take rule lists get RoutingRUDirect.
	RoutingBlocked Routing = "blocked"
	DefaultRouting         = RoutingRUDirect
)

// ParseRouting maps a stored setting to a mode; empty and unknown values get the default.
func ParseRouting(s string) Routing {
	if r := Routing(s); r == RoutingRUDirect || r == RoutingAll || r == RoutingBlocked {
		return r
	}
	return DefaultRouting
}

// geoxURL is the geodata Koala Clash ships (MetaCubeX meta-rules-dat, mihomo's own
// default). Apps without the files download them from here before the profile starts.
var geoxURL = map[string]string{
	"geoip":   "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip-lite.dat",
	"geosite": "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geosite.dat",
	"mmdb":    "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/geoip.metadb",
	"asn":     "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/GeoLite2-ASN.mmdb",
}

// WithDefaults fills in the names the admin left empty, in the panel's default language
// ("en", Russian otherwise).
func (g Groups) WithDefaults(lang string) Groups {
	if g.Main == "" {
		g.Main = "VPN"
	}
	if g.Auto == "" {
		g.Auto = "Авто"
		if lang == "en" {
			g.Auto = "Auto"
		}
	}
	return g
}

// reserved names are mihomo's built-in policies; a group or proxy with such a name
// breaks the profile.
var reserved = []string{"DIRECT", "REJECT", "REJECT-DROP", "PASS", "COMPATIBLE", "GLOBAL"}

// ValidName checks a group or proxy name the admin typed. Commas are refused because
// rules reference groups as "MATCH,<name>".
func ValidName(s string) error {
	switch {
	case s == "" || strings.TrimSpace(s) != s:
		return errors.New("name_blank")
	case utf8.RuneCountInString(s) > 48:
		return errors.New("name_too_long")
	case strings.ContainsAny(s, ",\"'\\"):
		return errors.New("name_bad_char")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return errors.New("name_bad_char")
		}
	}
	for _, x := range reserved {
		if strings.EqualFold(s, x) {
			return errors.New("name_reserved")
		}
	}
	return nil
}

// ErrNoProxies: nothing of the profile is left for the app, or for any app.
var ErrNoProxies = errors.New("no proxies")

// Bypass (AoiVPN fork): external bypass proxies and the name of their group. These are
// NOT mikan inbounds/slots — they are injected into the sub as extra proxies + a second
// select group ("🇷🇺 Обход БС"), fed from the sub_bypass setting. Not tracked by the panel.
type Bypass struct {
	Group   string           `json:"group"`
	Proxies []map[string]any `json:"proxies"`
}

type Profile struct {
	// Skip, when set, hears of an inbound that is left out because it cannot be rendered
	// (a port that is no number, a template that no longer parses): one broken inbound must
	// not empty or fail the subscription of everyone.
	Skip     func(in db.Inbound, err error)
	Slot     db.Slot
	Inbounds []db.Inbound // enabled and allowed for this user, in display order
	Nodes    []Node       // enabled nodes in display order; inbounds of other nodes are skipped
	Direct   []string     // hosts that bypass the tunnel: the panel and every node
	// Fingerprint is the panel's default uTLS profile (proto.ClientInput.Fingerprint).
	Fingerprint string
	// Rules are the admin's own Clash rules (ServedRules), before the built-in routing.
	Rules []string
	// Bypass (AoiVPN fork): external bypass proxies + group name, injected into the sub.
	Bypass *Bypass
	// NodeOrder/ProtoOrder (AoiVPN fork): custom display order — nodes by a case-sensitive
	// substring of their name, inbounds by preset id. Empty keeps the by-id order.
	NodeOrder  []string
	ProtoOrder []string
	// Routes go after the admin's rules, for the apps that take rule lists (App); Lang
	// names the groups they add.
	Routes Routes
	App    App
	Lang   string
}

type proxy struct {
	name string
	node int64
	uri  string
	yaml map[string]any
}

// NodePrefix is what precedes proxy names of a node when a subscription has several:
// the flag a name starts with, or the whole name.
func NodePrefix(name string) string {
	rs := []rune(strings.TrimSpace(name))
	if len(rs) >= 2 && isRegional(rs[0]) && isRegional(rs[1]) {
		return string(rs[:2])
	}
	return string(rs)
}

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// build renders the user's proxies node by node, in the nodes' order.
func build(p Profile) ([]proxy, error) {
	var out []proxy
	used := map[string]bool{}
	slot := proto.Slot{Name: p.Slot.Name, UUID: p.Slot.Uuid, Secret: p.Slot.Secret}
	orderNodes(p.Nodes, p.NodeOrder)
	orderInbounds(p.Inbounds, p.ProtoOrder)
	multi := len(p.Nodes) > 1
	for _, n := range p.Nodes {
		for _, in := range p.Inbounds {
			if in.NodeID != n.ID {
				continue
			}
			port, err := firstPort(in.Port)
			if err != nil {
				p.skip(in, err)
				continue
			}
			t, err := parseTemplate(in.Config)
			if err != nil {
				// Saved configs are validated; one broken inbound must not empty the subscription.
				p.skip(in, err)
				continue
			}
			// AoiVPN fork: shadowsocks is off the subscription by the owner's decision
			// (ea9a6da). Its inbounds may still live in the panel; they are not served.
			if pt, _ := t["type"].(string); pt == "shadowsocks" {
				continue
			}
			spec := in.Port
			if n.Endpoint.SNIRouter && proto.SNI443(t) {
				port, spec = 443, "443"
			}
			base := domain.ProxyName(in)
			// A name the admin typed is used as is; preset names get the node's flag.
			if prefix := NodePrefix(n.Name); multi && in.DisplayName == "" && prefix != "" {
				base = prefix + " " + base
			}
			// mihomo refuses a profile with two proxies of the same name.
			name := base
			for i := 2; used[name]; i++ {
				name = base + " " + strconv.Itoa(i)
			}
			c, err := proto.ClientConfig(t, proto.ClientInput{Name: name, Host: n.Endpoint.Host, Port: port, PortSpec: spec,
				SNI: n.Endpoint.SNI, PinSHA256: n.Endpoint.PinSHA256, Fingerprint: p.Fingerprint, Slot: slot})
			if err != nil {
				p.skip(in, err)
				continue
			}
			used[name] = true
			out = append(out, proxy{name: name, node: n.ID, uri: c.URI, yaml: c.Mihomo})
		}
	}
	return out, nil
}

func (p Profile) skip(in db.Inbound, err error) {
	if p.Skip != nil {
		p.Skip(in, err)
	}
}

func firstPort(spec string) (int, error) {
	head, _, _ := strings.Cut(spec, ",")
	head, _, _ = strings.Cut(head, "-")
	p, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil || p <= 0 || p > 65535 {
		return 0, fmt.Errorf("bad port %q", spec)
	}
	return p, nil
}

// URIs renders one share link per inbound.
func URIs(p Profile) (string, error) {
	ps, err := build(p)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(ps))
	for _, x := range ps {
		if x.uri != "" { // the mihomo-only types have no share link
			lines = append(lines, x.uri)
		}
	}
	if len(lines) == 0 {
		return "", ErrNoProxies
	}
	return strings.Join(lines, "\n"), nil
}

// Mihomo renders a complete client profile.
func Mihomo(p Profile, g Groups, r Routing) ([]byte, error) {
	cfg, _, err := mihomoConfig(p, g, r)
	if err != nil {
		return nil, err
	}
	return marshalYAML(cfg)
}

// mihomoConfig is the profile Mihomo renders, and the proxies in it.
func mihomoConfig(p Profile, g Groups, r Routing) (map[string]any, []proxy, error) {
	ps, err := build(p)
	if err != nil {
		return nil, nil, err
	}
	if len(ps) == 0 {
		// Groups with no proxies in them are a profile mihomo may refuse whole.
		return nil, nil, ErrNoProxies
	}
	return mihomoConfigOf(p, g, r, ps), ps, nil
}

// defaultNameservers are the DNS-over-HTTPS servers a profile resolves with, and the ones that
// resolve the proxy servers' own names.
var defaultNameservers = []string{"https://1.1.1.1/dns-query", "https://dns.google/dns-query"}

// mihomoConfigOf is the profile of ps.
func mihomoConfigOf(p Profile, g Groups, r Routing, ps []proxy) map[string]any {
	g = g.WithDefaults("")
	proxies := make([]map[string]any, len(ps))
	names := make([]string, len(ps))
	for i, x := range ps {
		proxies[i], names[i] = x.yaml, x.name
	}
	// AoiVPN fork: flat two-group layout — one select group (g.Main) with the panel's own
	// proxies (nodes only) and an optional "🇷🇺 Обход БС" select group of the injected
	// bypass proxies. No url-test, per-country or per-service groups, whatever the
	// routing page's "a group per node" switch says: the fork's subscription stays flat.
	// The main group holds the node proxies and, last, a reference to the bypass group —
	// NOT the bypass proxies themselves: there are hundreds of them, and picking one from
	// a 500-entry list buries the nodes.
	var bnames []string
	if p.Bypass != nil && len(p.Bypass.Proxies) > 0 {
		for _, bp := range p.Bypass.Proxies {
			proxies = append(proxies, bp)
			if n, ok := bp["name"].(string); ok {
				bnames = append(bnames, n)
			}
		}
	}
	mainMembers := append([]string{}, names...)
	if p.Bypass != nil && len(p.Bypass.Proxies) > 0 {
		mainMembers = append(mainMembers, p.Bypass.Group)
	}
	var groups []map[string]any
	if p.Bypass != nil && len(p.Bypass.Proxies) > 0 {
		groups = append(groups, selectGroup(g.Main, mainMembers, g.Icon), selectGroup(p.Bypass.Group, bnames, ""))
	} else {
		groups = append(groups, selectGroup(g.Main, mainMembers, g.Icon))
	}
	taken := map[string]bool{}
	for _, n := range append(append([]string{}, names...), g.Main, g.Auto, AliasGroup) {
		taken[strings.ToLower(n)] = true
	}
	if !p.routable() && r == RoutingBlocked {
		r = RoutingRUDirect
	}
	rt := p.route(g, ps, taken, r)
	groups = append(groups, rt.groups...)
	dns := map[string]any{
		"enable": true, "ipv6": false, "enhanced-mode": "fake-ip", "fake-ip-range": "198.18.0.1/16",
		"default-nameserver": []string{"1.1.1.1", "8.8.8.8"},
		"nameserver":         slices.Clone(defaultNameservers),
		// respect-rules: DNS for a domain follows the same routing rule as its traffic, so
		// a proxied domain is resolved through the tunnel and a direct one directly — no
		// mismatch where the handshake waits on a wrongly-routed DNS answer. prefer-h3:
		// resolve the DoH servers over HTTP/3, which opens faster. Together these are what
		// cut the handshake delay (the client's "Соблюдать правила" + "PreferH3"); unlike a
		// sniffer they do not touch routing, so RU-direct rules and Hysteria2 keep working.
		"respect-rules": true,
		"prefer-h3":     true,
		// fake-ip-filter: domains that get a REAL IP instead of a fake 198.18.x one.
		// Without it, apps excluded from the tunnel (per-app bypass) still get a fake IP
		// from mihomo's DNS and can't connect at all (seen with Roblox — fails even when
		// bypassed). Covers local/captive/connectivity-check + STUN + game/CDN hosts that
		// break under fake-ip. No sniffer here — it does not touch routing, so Hysteria2
		// and RU-direct keep working.
		"fake-ip-filter": []string{
			"*.lan", "*.local", "*.localhost", "*.arpa",
			"time.*.com", "time.*.gov", "time.windows.com", "ntp.*.com",
			"*.msftconnecttest.com", "*.msftncsi.com",
			"connectivitycheck.*.com", "connectivitycheck.gstatic.com",
			"captive.apple.com", "*.push.apple.com",
			"stun.*.*", "*.stun.*.*",
			"+.roblox.com", "+.rbxcdn.com", "+.rbxcdn.net", "+.rbxinfra.com",
			"+.rbxgear.com", "+.robloxlabs.com", "+.rbx.com",
		},
	}
	// The panel and the nodes stay out of the tunnel whatever the admin's rules say.
	// AoiVPN fork (ea9a6da): the control domains go THROUGH the tunnel, ahead of every
	// GEOSITE/GEOIP-ru rule — otherwise ".ru → DIRECT" sends the subscription update over
	// RU DNS/TSPU and the app fails with "Failed host lookup: sub.aoimusic.ru". They come
	// right after directRules: templates cut the rules at len(directRules(p.Direct)).
	rules := append(directRules(p.Direct),
		"DOMAIN-SUFFIX,sub.aoimusic.ru,"+g.Main,
		"DOMAIN-SUFFIX,panel.aoimusic.ru,"+g.Main,
		"DOMAIN-SUFFIX,vpn.aoimusic.ru,"+g.Main,
		"GEOIP,LAN,DIRECT,no-resolve")
	tune := p.Routes.Tune
	if tune.BlockQUIC {
		rules = append(rules, "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT")
	}
	rules = append(rules, p.Rules...)
	rules = append(rules, rt.rules...)
	cfg := map[string]any{
		"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning",
		// The node has no IPv6 on most VPS: with it on, apps first try IPv6 through the
		// tunnel and wait for the node's "network unreachable" before falling back.
		"ipv6": false, "unified-delay": true, "tcp-concurrent": true,
		// find-process-mode: always — mihomo matches PROCESS-NAME rules (games/torrents
		// → DIRECT). Without it those rules never fire, so e.g. Roblox is forced through
		// the tunnel and its datacentre-IP block breaks the game. "strict" would break
		// the match, so "always" like the legacy subscription.
		"find-process-mode": "always",
		"dns":               dns,
		"proxies":           proxies,
		"proxy-groups":      groups,
	}
	if len(rt.providers) > 0 {
		cfg["rule-providers"] = rt.providers
	}
	if rt.geodata || r != RoutingAll {
		cfg["geodata-mode"], cfg["geox-url"] = false, geoxURL
	}
	if r == RoutingRUDirect {
		rules = append(rules, "GEOSITE,category-ru,DIRECT", "GEOIP,ru,DIRECT")
	}
	if r != RoutingAll {
		// GEOIP,ru makes the app resolve every domain itself. DoH straight from Russia
		// stalls under TSPU throttling, so it goes through the tunnel via the main group's
		// "#group" suffix. It MUST be the real group name (g.Main, e.g. "AoiVPN"), not the
		// AliasGroup constant "PROXY": a renamed main group with "#PROXY" points at a group
		// that does not exist, so mihomo cannot route DNS through the tunnel and falls back
		// to resolving straight from the device (RU DNS) — a region leak. A '#' in a renamed
		// group would break the suffix, so fall back to the alias only then.
		// Russian domains resolve with Yandex DNS directly and keep working without the VPN.
		dnsGroup := g.Main
		if strings.ContainsAny(dnsGroup, "#") {
			dnsGroup = AliasGroup
		}
		dns["nameserver"] = []string{"https://1.1.1.1/dns-query#" + dnsGroup, "https://8.8.8.8/dns-query#" + dnsGroup}
		dns["proxy-server-nameserver"] = slices.Clone(defaultNameservers)
		// AoiVPN fork (ea9a6da): the control domains resolve through the tunnel, not RU DNS.
		dns["nameserver-policy"] = map[string]any{
			"+.aoimusic.ru":       []string{"https://1.1.1.1/dns-query#" + dnsGroup},
			"geosite:category-ru": []string{"77.88.8.8", "77.88.8.1"},
		}
	}
	p.Routes.DNS.apply(dns)
	if tune.RealIP {
		dns["enhanced-mode"] = "redir-host"
		delete(dns, "fake-ip-range")
	}
	if tune.Sniffer {
		cfg["sniffer"] = map[string]any{"enable": true, "parse-pure-ip": true,
			"sniff": map[string]any{"HTTP": map[string]any{"ports": []any{80, "8080-8880"}}, "TLS": map[string]any{"ports": []any{443, 8443}},
				"QUIC": map[string]any{"ports": []any{443, 8443}}}}
	}
	// mihomo skips a rule whose group can't carry UDP (a proxy with udp off, picked by hand
	// or by url-test) and sends what falls through to DIRECT: past the tunnel, from the real
	// address. REJECT makes such apps fall back to TCP instead. In the "blocked" mode the
	// rest goes direct by design; its tunnel rules get their own REJECT twins (blockedRules).
	last := []string{"MATCH," + g.Main, "MATCH,REJECT"}
	if r == RoutingBlocked {
		last = []string{"MATCH,DIRECT"}
	}
	cfg["rules"] = append(rules, last...)
	return cfg
}

func urlTest(name string, proxies []string) map[string]any {
	return map[string]any{"name": name, "type": "url-test", "proxies": proxies, "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50}
}

// selectGroup (AoiVPN fork): a manual select group with an optional mihomo icon URL.
func selectGroup(name string, proxies []string, icon string) map[string]any {
	grp := map[string]any{"name": name, "type": "select", "proxies": proxies}
	if icon != "" {
		grp["icon"] = icon
	}
	return grp
}

// sortByOrder (AoiVPN fork): stable-sort items by the first `order` entry they match
// (substring for node names, exact for presets); unknown items keep to the end.
func sortByOrder[T any](items []T, order []string, keyOf func(T) string, contains bool) {
	if len(order) == 0 {
		return
	}
	idx := func(s string) int {
		for i, o := range order {
			if (contains && strings.Contains(s, o)) || (!contains && s == o) {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(items, func(i, j int) bool { return idx(keyOf(items[i])) < idx(keyOf(items[j])) })
}

func orderNodes(nodes []Node, order []string) {
	sortByOrder(nodes, order, func(n Node) string { return n.Name }, true)
}

func orderInbounds(ins []db.Inbound, order []string) {
	sortByOrder(ins, order, func(in db.Inbound) string { return in.Preset }, false)
}

// directRules keep traffic to the server itself (panel, SSH, subscription updates) out
// of the tunnel: in TUN mode it would otherwise loop through the node and die with it.
func directRules(hosts []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		switch ip, err := netip.ParseAddr(h); {
		case err != nil:
			out = append(out, "DOMAIN,"+h+",DIRECT")
		case ip.Is4():
			out = append(out, "IP-CIDR,"+ip.String()+"/32,DIRECT,no-resolve")
		default:
			out = append(out, "IP-CIDR6,"+ip.String()+"/128,DIRECT,no-resolve")
		}
	}
	return out
}
