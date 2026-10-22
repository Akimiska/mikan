// Package netaddr says where an address leads: to the internet, or to this host, its network,
// a carrier's NAT or nowhere a unicast dial can go. Every check of mikan that asks whether an
// address is public is Classify, each with its own policy on top: what a CA can validate, what
// the panel may dial for the admin, what an import may reach.
package netaddr

import (
	"net/netip"
	"strings"
)

// Kind is what an address is.
type Kind int

const (
	Public      Kind = iota
	Invalid          // not an address at all
	Unspecified      // 0.0.0.0, ::
	Loopback         // 127.0.0.0/8, ::1
	Private          // RFC 1918, fc00::/7
	Shared           // 100.64.0.0/10: carrier-grade NAT (RFC 6598), where Tailscale puts its peers
	LinkLocal        // 169.254.0.0/16, fe80::/10: the cloud's metadata service lives here
	Multicast
	Broadcast // 255.255.255.255
)

var (
	shared    = netip.MustParsePrefix("100.64.0.0/10")
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	broadcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})
)

// Unwrap is the address a dial judges: an IPv4-mapped one as IPv4, and an IPv4 address inside
// the NAT64 prefix as itself, so 64:ff9b::a9fe:a9fe does not reach 169.254.169.254.
func Unwrap(a netip.Addr) netip.Addr {
	a = a.Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return a
}

// Classify says what a is, judged as Unwrap has it.
func Classify(a netip.Addr) Kind {
	if !a.IsValid() {
		return Invalid
	}
	a = Unwrap(a)
	switch {
	case a.IsUnspecified():
		return Unspecified
	case a.IsLoopback():
		return Loopback
	case a.IsPrivate():
		return Private
	case shared.Contains(a):
		return Shared
	case a.IsLinkLocalUnicast():
		return LinkLocal
	case a.IsMulticast():
		return Multicast
	case a == broadcast:
		return Broadcast
	}
	return Public
}

// IsPublic: an address on the internet, not one of this host's, its network's or a carrier's
// NAT, and not one no unicast dial goes to.
func IsPublic(a netip.Addr) bool { return Classify(a) == Public }

// PublicHost is the syntax of a host that may be dialed on behalf of the admin: DNS names
// and public addresses pass, literal private addresses, localhost in any spelling and bare
// names do not. It does not resolve: a name that leads to a private address passes here,
// and whoever dials it checks the address it gets (scan.Check, Inbounds.CheckTemplate).
func PublicHost(h string) bool {
	// "localhost." and "LOCALHOST" are the same name; so is anything under .localhost.
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(h, "[]"), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || !strings.Contains(h, ".") && !strings.Contains(h, ":") {
		return false
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return true
	}
	return IsPublic(a)
}
