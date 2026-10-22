package netaddr

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	for s, want := range map[string]Kind{
		"8.8.8.8":                Public,
		"203.0.113.20":           Public,
		"2606:4700::1111":        Public,
		"64:ff9b::808:808":       Public, // 8.8.8.8 over NAT64
		"0.0.0.0":                Unspecified,
		"::":                     Unspecified,
		"127.0.0.1":              Loopback,
		"127.8.9.10":             Loopback,
		"::1":                    Loopback,
		"::ffff:127.0.0.1":       Loopback,
		"10.1.2.3":               Private,
		"172.16.0.1":             Private,
		"192.168.0.1":            Private,
		"fd00:ec2::254":          Private,
		"100.64.0.1":             Shared,
		"100.127.255.254":        Shared,
		"::ffff:100.100.1.1":     Shared,
		"100.128.0.1":            Public, // just past the carriers' range
		"169.254.169.254":        LinkLocal,
		"fe80::1":                LinkLocal,
		"64:ff9b::a9fe:a9fe":     LinkLocal, // 169.254.169.254 over NAT64
		"::ffff:169.254.1.1":     LinkLocal,
		"224.0.0.1":              Multicast,
		"ff02::1":                Multicast,
		"255.255.255.255":        Broadcast,
		"64:ff9b::7f00:1":        Loopback, // 127.0.0.1 over NAT64
		"64:ff9b::a00:1":         Private,  // 10.0.0.1 over NAT64
		"64:ff9b::6440:1":        Shared,   // 100.64.0.1 over NAT64
		"2001:db8::1":            Public,   // documentation, but a unicast address all the same
		"fe80::1%eth0":           LinkLocal,
		"::ffff:255.255.255.255": Broadcast,
	} {
		if got := Classify(netip.MustParseAddr(s)); got != want {
			t.Errorf("Classify(%s) = %d, want %d", s, got, want)
		}
	}
	if Classify(netip.Addr{}) != Invalid {
		t.Error("the zero Addr is an address")
	}
}

func TestPublicHost(t *testing.T) {
	for host, want := range map[string]bool{
		"www.example.org":     true,
		"www.example.org.":    true,
		"203.0.113.20":        true,
		"2001:db8::1":         true,
		"localhost":           false,
		"localhost.":          false,
		"LOCALHOST":           false,
		"LocalHost.":          false,
		"app.localhost":       false,
		"app.localhost.":      false,
		"intranet":            false,
		"127.0.0.1":           false,
		"[::1]":               false,
		"::ffff:127.0.0.1":    false,
		"10.1.2.3":            false,
		"192.168.0.1":         false,
		"172.16.0.1":          false,
		"169.254.169.254":     false,
		"100.64.0.1":          false,
		"0.0.0.0":             false,
		"224.0.0.1":           false,
		"fe80::1":             false,
		"fd00::1":             false,
		"ff02::1":             false,
		"::ffff:169.254.1.1":  false,
		"64:ff9b::a9fe:a9fe":  false,
		"255.255.255.255":     false,
		"Example.COM.":        true,
		"8.8.8.8":             true,
		"[2606:4700::1111]":   true,
		"127.0.0.1.":          false,
		"::1":                 false,
		"":                    false,
		"2001:db8::1%25eth0x": true, // a zone, not a host of this network: the dialer resolves and checks
	} {
		if got := PublicHost(host); got != want {
			t.Errorf("PublicHost(%q) = %v, want %v", host, got, want)
		}
	}
}
