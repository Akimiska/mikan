package nodesync

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

const (
	// panelAddrsTTL is how long the panel's own addresses are kept: they are looked up when a
	// node's state is built, which is seldom, and a panel that moves is let in by the node.
	panelAddrsTTL  = 10 * time.Minute
	panelAddrsWait = 3 * time.Second
)

// apiAllow is the API lock of a remote node that has it on (nodeapi.DesiredState.APIAllow):
// the panel's own addresses and the networks the admin added; nil while it is off. The node
// adds the address the panel reaches it from, so a panel whose connections leave from
// another address (NAT, a second IP) is not locked out by this guess.
func (s *Syncer) apiAllow(ctx context.Context, n db.Node) []string {
	if n.ApiLockdown == 0 || n.Address == "" {
		return nil
	}
	out := append(slices.Clone(n.ApiAllow), s.m.panelNets(ctx)...)
	slices.Sort(out)
	return append([]string{}, slices.Compact(out)...)
}

// panelNets are the panel's public address and its domain as networks of one address each:
// what they are, or resolve to. A name that does not resolve leaves nothing.
func (m *Manager) panelNets(ctx context.Context) []string {
	m.addrMu.Lock()
	defer m.addrMu.Unlock()
	now := m.now()
	if !m.addrAt.IsZero() && now.Sub(m.addrAt) < panelAddrsTTL {
		return slices.Clone(m.addrs)
	}
	lookup := m.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	var out []string
	for _, key := range []string{settings.KeyPublicHost, settings.KeyDomain} {
		host, err := m.set.String(ctx, key)
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if err != nil || host == "" {
			continue
		}
		addrs := []netip.Addr{}
		if a, err := netip.ParseAddr(host); err == nil {
			addrs = append(addrs, a)
		} else {
			wait, cancel := context.WithTimeout(ctx, panelAddrsWait)
			addrs, _ = lookup(wait, host)
			cancel()
		}
		for _, a := range addrs {
			if a = a.Unmap(); a.IsValid() && a.Zone() == "" {
				out = append(out, netip.PrefixFrom(a, a.BitLen()).String())
			}
		}
	}
	slices.Sort(out)
	m.addrs, m.addrAt = slices.Compact(out), now
	return slices.Clone(m.addrs)
}
