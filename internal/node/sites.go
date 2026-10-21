package node

import (
	"net"
	"net/netip"
	"strings"

	C "github.com/metacubex/mihomo/constant"
	"golang.org/x/net/publicsuffix"
)

// Where users go, for the admin who turned it on (DesiredState.Detail, off by default): the
// bytes of each slot by site, the registrable domain of the name the connection was for
// (SNI, Host, the sniffer's) or the address when there is none. Nothing else is kept: no
// times, no source addresses, no single connection. The sums go with the counters' batch.
// The core reports every connection and UDP session of a user as it ends, with the name
// and both ways' bytes (left); nothing wraps the connections, so they may still be spliced.

// maxSites is how many sites one slot keeps between two batches; the rest add up under
// otherSite, so a user who walks the whole internet costs the node no more than this.
const (
	maxSites  = 256
	otherSite = "*"
)

// siteOf is the site a connection to host (a name, or "" when only addr is known) counts
// to: example.com for www.example.com, the address itself for a bare IP.
func siteOf(host string, addr netip.Addr) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || net.ParseIP(host) != nil {
		if addr.IsValid() {
			return addr.Unmap().String()
		}
		return otherSite
	}
	if len(host) > 253 {
		return otherSite
	}
	if site, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return site
	}
	return host
}

// countSite adds a connection's bytes to the slot's site, when detail is on.
func (r *Registry) countSite(s *slot, site string, up, down int64) {
	if !r.detail.Load() || up == 0 && down == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sites == nil {
		s.sites = map[string]*[2]int64{}
	}
	t, ok := s.sites[site]
	if !ok {
		if len(s.sites) >= maxSites {
			site = otherSite
			t = s.sites[site]
		}
		if t == nil {
			t = &[2]int64{}
			s.sites[site] = t
		}
	}
	t[0] += up
	t[1] += down
}

// SetDetail turns the sites' sums on or off; off drops what was summed.
func (r *Registry) SetDetail(on bool) {
	if r.detail.Swap(on) == on || on {
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.byName {
		s.mu.Lock()
		s.sites = nil
		s.mu.Unlock()
	}
}

// left counts a connection or UDP session the core is done with to its slot's site, when
// detail is on (statistic.Manager.SetOnLeave). With no user it is a listener's one key for
// everyone; a REALITY handshake to its dest or another node's relay has no slot.
func (r *Registry) left(m *C.Metadata, up, down int64) {
	if !r.detail.Load() || m == nil {
		return
	}
	s := r.lookup(m.InUser)
	if m.InUser == "" {
		s = r.sharedSlot(m.InName)
	}
	if s == nil {
		return
	}
	r.countSite(s, siteOf(m.RuleHost(), m.DstIP), up, down)
}
