package acme

import "testing"

// A CA validates only what it can reach from the internet: a carrier's NAT and Tailscale's
// 100.64.0.0/10 are no more that than a LAN address, so no order is placed for them.
func TestIsPrivate(t *testing.T) {
	for id, want := range map[string]bool{
		"100.64.0.10":        true,
		"100.100.1.1":        true,
		"10.0.0.1":           true,
		"192.168.1.5":        true,
		"127.0.0.1":          true,
		"169.254.169.254":    true,
		"0.0.0.0":            true,
		"::1":                true,
		"fd00::1":            true,
		"64:ff9b::a9fe:a9fe": true,
		"panel":              true, // a docker service name, not a DNS name
		"203.0.113.20":       false,
		"8.8.8.8":            false,
		"2606:4700::1111":    false,
		"vpn.example.com":    false,
	} {
		if got := isPrivate(id); got != want {
			t.Errorf("isPrivate(%q) = %v, want %v", id, got, want)
		}
	}
}
