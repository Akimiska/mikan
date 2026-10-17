package node

import (
	"net/netip"
	"slices"

	"mikan/internal/nodeapi"
)

// egressRules refuse users' traffic to the ports, networks and domains of the egress
// filter. They come after the node's own REJECT rules and before the cascade and WARP, so
// no way out reaches what the filter closes. Each entry is read as the panel reads it
// and written in that canonical form; what does not parse is left out: mihomo refuses
// the whole config over one rule it cannot parse, and an older or broken panel must not
// take the node's users down with it.
func egressRules(f *nodeapi.Filters) []string {
	if f == nil {
		return nil
	}
	var r []string
	for _, p := range f.Egress.Ports {
		if c, ok := nodeapi.CanonPorts(p); ok {
			r = append(r, "DST-PORT,"+c+",REJECT")
		}
	}
	for _, n := range f.Egress.Networks {
		p, ok := nodeapi.CanonNetwork(n)
		if !ok {
			continue
		}
		kind := "IP-CIDR"
		if p.Addr().Is6() {
			kind = "IP-CIDR6"
		}
		// Without no-resolve, as the private ranges: a domain that resolves into the
		// network is refused too.
		r = append(r, kind+","+p.String()+",REJECT")
	}
	for _, d := range f.Egress.Domains {
		if c, ok := nodeapi.CanonDomain(d); ok {
			r = append(r, "DOMAIN-SUFFIX,"+c+",REJECT")
		}
	}
	return r
}

// ingress is the ingress filter as the tunnel checks it: the networks as sorted ranges
// that do not overlap, so a check is a binary search. It runs for every UDP packet, and
// a list may hold a thousand networks.
type ingress struct {
	allow  bool
	ranges []addrRange
}

type addrRange struct{ first, last netip.Addr }

func newIngress(allow bool, nets []netip.Prefix) *ingress {
	rs := make([]addrRange, 0, len(nets))
	for _, p := range nets {
		p = p.Masked()
		rs = append(rs, addrRange{p.Addr(), lastAddr(p)})
	}
	// netip.Addr orders IPv4 before IPv6, so one sorted list holds both.
	slices.SortFunc(rs, func(a, b addrRange) int { return a.first.Compare(b.first) })
	in := &ingress{allow: allow}
	for _, r := range rs {
		if n := len(in.ranges); n > 0 && r.first.Compare(in.ranges[n-1].last) <= 0 {
			if r.last.Compare(in.ranges[n-1].last) > 0 {
				in.ranges[n-1].last = r.last
			}
			continue
		}
		in.ranges = append(in.ranges, r)
	}
	return in
}

// lastAddr is the last address of a masked network.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	for i := range b {
		switch n := p.Bits() - i*8; {
		case n <= 0:
			b[i] = 0xff
		case n < 8:
			b[i] |= 0xff >> n
		}
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

func (in *ingress) admits(a netip.Addr) bool {
	a = a.Unmap()
	// i is the first range that starts after a: only the one before it can hold a.
	i, _ := slices.BinarySearchFunc(in.ranges, a, func(r addrRange, a netip.Addr) int {
		if r.first.Compare(a) <= 0 {
			return -1
		}
		return 1
	})
	if i > 0 && a.Compare(in.ranges[i-1].last) <= 0 {
		return in.allow
	}
	return !in.allow
}

// SetIngress takes the ingress filter of the state; without networks it lets everyone in.
func (r *Registry) SetIngress(f *nodeapi.Filters) {
	if f == nil {
		r.ingress.Store(nil)
		return
	}
	var nets []netip.Prefix
	for _, n := range f.Ingress.Networks {
		if p, ok := nodeapi.CanonNetwork(n); ok {
			nets = append(nets, p)
		}
	}
	// An allow list with nothing that parses would shut every user out; nothing listed
	// is no filter.
	if len(nets) == 0 {
		r.ingress.Store(nil)
		return
	}
	r.ingress.Store(newIngress(f.Ingress.Allow, nets))
}

// admitFrom says whether the ingress filter lets a connection from the address in.
func (r *Registry) admitFrom(a netip.Addr) bool {
	in := r.ingress.Load()
	return in == nil || in.admits(a)
}
