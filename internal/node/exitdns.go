package node

import (
	"encoding/json"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/sniffer"
	"github.com/metacubex/mihomo/config"
	"github.com/metacubex/mihomo/dns"
	"github.com/metacubex/mihomo/tunnel"

	"mikan/internal/nodeapi"
)

// The DNS at the exit (nodeapi.ExitDNS). mihomo sets its sniffer and resolver once, with
// the whole config; the node applies its config once at the start and patches listeners,
// proxies and rules after, so these two are swapped here when the state changes them.

// snifferConfig reads the name out of a connection made to a bare address and dials the
// name (override-destination): the node resolves it, not the user's device. Only on the
// ports of SniffPorts, where the client speaks first.
func snifferConfig(d *nodeapi.ExitDNS) map[string]any {
	if d == nil || !d.Sniff {
		return map[string]any{"enable": false}
	}
	ports := func(ps []string) []any {
		out := make([]any, 0, len(ps))
		for _, p := range ps {
			out = append(out, p)
		}
		return out
	}
	return map[string]any{
		"enable":               true,
		"parse-pure-ip":        true,
		"override-destination": true,
		"sniff": map[string]any{
			"TLS":  map[string]any{"ports": ports(nodeapi.SniffPorts.TLS)},
			"HTTP": map[string]any{"ports": ports(nodeapi.SniffPorts.HTTP)},
			"QUIC": map[string]any{"ports": ports(nodeapi.SniffPorts.QUIC)},
		},
	}
}

// dnsConfig is mihomo's resolver with the state's servers; without any, the host's own
// (mihomo's DNS off). A server that does not parse is left out, as the egress rules are:
// a broken or older panel must not take the node's users down with it. A resolver by
// name finds its address with the host's resolver.
func dnsConfig(d *nodeapi.ExitDNS) map[string]any {
	var servers []string
	if d != nil {
		for _, s := range d.Servers {
			if c, ok := nodeapi.CanonDNSServer(s); ok && len(servers) < nodeapi.MaxDNSServers {
				servers = append(servers, c)
			}
		}
	}
	if len(servers) == 0 {
		return map[string]any{"enable": false}
	}
	return map[string]any{
		"enable":             true,
		"ipv6":               true,
		"enhanced-mode":      "normal",
		"default-nameserver": []string{"system://"},
		"nameserver":         servers,
	}
}

// exitDNSKey covers what applyExitDNS depends on.
func exitDNSKey(d *nodeapi.ExitDNS) string {
	raw, _ := json.Marshal(struct {
		S map[string]any
		D map[string]any
	}{snifferConfig(d), dnsConfig(d)})
	return string(raw)
}

// applyExitDNS puts the parsed config's sniffer and resolver in place, as mihomo's
// executor does on a full reload (updateSniffer, updateDNS) but without the DNS server:
// the node listens for nobody's DNS.
func applyExitDNS(cfg *config.Config) error {
	d, err := sniffer.NewDispatcher(cfg.Sniffer)
	if err != nil {
		return err
	}
	tunnel.UpdateSniffer(d)

	c := cfg.DNS
	if !c.Enable {
		resolver.DefaultResolver = nil
		resolver.DefaultHostMapper = nil
		resolver.DefaultService = nil
		resolver.ProxyServerHostResolver = nil
		resolver.DirectHostResolver = nil
		return nil
	}
	r := dns.NewResolver(dns.Config{
		Main:           c.NameServer,
		IPv6:           c.IPv6,
		IPv6Timeout:    c.IPv6Timeout,
		Default:        c.DefaultNameserver,
		CacheAlgorithm: c.CacheAlgorithm,
		CacheMaxSize:   c.CacheMaxSize,
	})
	m := dns.NewEnhancer(dns.EnhancerConfig{IPv6: c.IPv6, EnhancedMode: c.EnhancedMode, UseHosts: c.UseHosts})
	if old, ok := resolver.DefaultHostMapper.(*dns.ResolverEnhancer); ok && old != nil {
		m.PatchFrom(old)
	}
	resolver.DefaultResolver = r
	resolver.DefaultHostMapper = m
	resolver.DefaultService = dns.NewService(r, m)
	resolver.UseSystemHosts = c.UseSystemHosts
	resolver.ProxyServerHostResolver = r.Resolver
	resolver.DirectHostResolver = r.Resolver
	return nil
}
