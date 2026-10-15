// Package exitdns holds the DNS at the exit (nodeapi.ExitDNS): the nodes name the sites
// users reach themselves, so a site sees the node's country whatever DNS the user's
// device asked. On from the start: a panel updated to it turns it on for every node.
package exitdns

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/settings"
)

const KeyConfig = "exit_dns"

// Resolvers: the host's own, three public ones over DoH by address (no DNS needed to
// find them, and the host's resolver may answer wrong), or the admin's own list.
const (
	ResolverHost       = "host"
	ResolverCloudflare = "cloudflare"
	ResolverGoogle     = "google"
	ResolverQuad9      = "quad9"
	ResolverCustom     = "custom"
)

var Resolvers = []string{ResolverHost, ResolverCloudflare, ResolverGoogle, ResolverQuad9, ResolverCustom}

// Presets are the servers of the public resolvers.
var Presets = map[string][]string{
	ResolverCloudflare: {"https://1.1.1.1/dns-query", "https://1.0.0.1/dns-query"},
	ResolverGoogle:     {"https://8.8.8.8/dns-query", "https://8.8.4.4/dns-query"},
	ResolverQuad9:      {"https://9.9.9.9/dns-query", "https://149.112.112.112/dns-query"},
}

var ErrResolver = errors.New("exit_dns_resolver")

type Config struct {
	// Sniff: the node reads the site's name from a connection made to an address and
	// resolves it itself.
	Sniff    bool   `json:"sniff"`
	Resolver string `json:"resolver"`
	// Servers are the admin's own resolvers, kept when another resolver is picked.
	Servers []string `json:"servers"`
}

func Default() Config {
	return Config{Sniff: true, Resolver: ResolverHost, Servers: []string{}}
}

func Load(ctx context.Context, s *settings.Settings) (Config, error) {
	c, _, err := settings.GetOver(ctx, s, KeyConfig, Default())
	if c.Servers == nil {
		c.Servers = []string{}
	}
	if !slices.Contains(Resolvers, c.Resolver) {
		c.Resolver = ResolverHost
	}
	return c, err
}

// Problem is a server of the admin's list that is not one: its index and what it was.
type Problem struct {
	Index int
	Value string
}

func (p *Problem) Error() string {
	return "exit dns: servers[" + strconv.Itoa(p.Index) + "] " + p.Value
}

// Validate puts the servers in their canonical form, in the admin's order without
// repeats, and returns a *Problem for one that is not a resolver, ErrResolver for an
// unknown resolver or an own list that is empty or too long.
func (c *Config) Validate() error {
	if !slices.Contains(Resolvers, c.Resolver) {
		return ErrResolver
	}
	if len(c.Servers) > nodeapi.MaxDNSServers {
		return ErrResolver
	}
	out := make([]string, 0, len(c.Servers))
	for i, s := range c.Servers {
		v, ok := nodeapi.CanonDNSServer(s)
		if !ok {
			if r := []rune(s); len(r) > 64 {
				s = string(r[:64]) + "…"
			}
			return &Problem{Index: i, Value: s}
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	c.Servers = out
	if c.Resolver == ResolverCustom && len(c.Servers) == 0 {
		return ErrResolver
	}
	return nil
}

// ServerList is what the nodes ask: none for the host's own resolver.
func (c Config) ServerList() []string {
	switch c.Resolver {
	case ResolverCustom:
		return c.Servers
	case ResolverHost:
		return nil
	}
	return Presets[c.Resolver]
}

// State is what the nodes get: nil when the node is left as it was before the setting.
func (c Config) State() *nodeapi.ExitDNS {
	servers := c.ServerList()
	if !c.Sniff && len(servers) == 0 {
		return nil
	}
	return &nodeapi.ExitDNS{Sniff: c.Sniff, Servers: slices.Clone(servers)}
}
