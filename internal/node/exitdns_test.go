package node

import (
	"crypto/tls"
	"net"
	"net/netip"
	"testing"
	"time"

	N "github.com/metacubex/mihomo/common/net"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/component/sniffer"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/tunnel"

	"mikan/internal/nodeapi"
	"mikan/internal/proto"
)

func TestExitDNSConfigParses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dns     *nodeapi.ExitDNS
		sniff   bool
		servers int
	}{
		{"none", nil, false, 0},
		{"off", &nodeapi.ExitDNS{}, false, 0},
		{"sniff", &nodeapi.ExitDNS{Sniff: true}, true, 0},
		// What does not parse is left out; the rest reaches mihomo.
		{"servers", &nodeapi.ExitDNS{Sniff: true, Servers: []string{"https://1.1.1.1/dns-query", "tls://dns.google", "8.8.8.8", "dns.google", "https://1.1.1.1#PROXY", "dhcp://eth0"}}, true, 3},
		{"servers only", &nodeapi.ExitDNS{Servers: []string{"quic://dns.adguard-dns.com"}}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := cascadeState(t)
			st.DNS = tc.dns
			raw, _, err := buildConfig(st, proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, false, "")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := executor.ParseWithBytes(raw)
			if err != nil {
				t.Fatalf("mihomo refuses the config: %v", err)
			}
			// Off, mihomo fills in its own defaults, which nothing reads.
			if cfg.Sniffer.Enable != tc.sniff || tc.sniff && !cfg.Sniffer.ParsePureIp {
				t.Fatalf("sniffer: %+v", cfg.Sniffer)
			}
			if tc.sniff {
				if len(cfg.Sniffer.Sniffers) != 3 {
					t.Fatalf("sniffers: %+v", cfg.Sniffer.Sniffers)
				}
				for typ, s := range cfg.Sniffer.Sniffers {
					if !s.OverrideDest {
						t.Fatalf("%v does not override the destination", typ)
					}
				}
			}
			if cfg.DNS.Enable != (tc.servers > 0) || tc.servers > 0 && len(cfg.DNS.NameServer) != tc.servers {
				t.Fatalf("dns: enable %v, %d servers", cfg.DNS.Enable, len(cfg.DNS.NameServer))
			}
			if tc.servers > 0 && cfg.DNS.Listen != "" {
				t.Fatal("the node listens for DNS")
			}
		})
	}
}

// A connection by address with a TLS hello leaves the sniffer dialing the name, which the
// node resolves: what the device's DNS answered is dropped.
func TestSnifferDialsTheName(t *testing.T) {
	st := cascadeState(t)
	st.DNS = &nodeapi.ExitDNS{Sniff: true}
	raw, _, err := buildConfig(st, proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := executor.ParseWithBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	d, err := sniffer.NewDispatcher(cfg.Sniffer)
	if err != nil {
		t.Fatal(err)
	}
	sniff := func(port uint16) *C.Metadata {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		go func() {
			_ = tls.Client(b, &tls.Config{ServerName: "gemini.google.com", MinVersion: tls.VersionTLS12}).Handshake()
		}()
		_ = a.SetDeadline(time.Now().Add(5 * time.Second))
		m := &C.Metadata{NetWork: C.TCP, Type: C.VLESS, DstIP: netip.MustParseAddr("203.0.113.5"), DstPort: port}
		d.TCPSniff(N.NewBufferedConn(a), m)
		return m
	}
	if m := sniff(443); m.Host != "gemini.google.com" || m.DstIP.IsValid() {
		t.Fatalf("443: host %q, ip %v", m.Host, m.DstIP)
	}
	// Not a port the client speaks first on: the address stays.
	if m := sniff(22); m.Host != "" || m.DstIP != netip.MustParseAddr("203.0.113.5") {
		t.Fatalf("22: host %q, ip %v", m.Host, m.DstIP)
	}
}

func TestExitDNSKey(t *testing.T) {
	if exitDNSKey(nil) != exitDNSKey(&nodeapi.ExitDNS{}) {
		t.Fatal("off and none differ")
	}
	if exitDNSKey(nil) == exitDNSKey(&nodeapi.ExitDNS{Sniff: true}) {
		t.Fatal("the sniffer does not change the key")
	}
	// A server that is left out does not change what mihomo holds.
	if exitDNSKey(&nodeapi.ExitDNS{Servers: []string{"8.8.8.8"}}) != exitDNSKey(&nodeapi.ExitDNS{Servers: []string{" 8.8.8.8", "junk"}}) {
		t.Fatal("the key depends on what is left out")
	}
}

// applyExitDNS swaps mihomo's sniffer and resolver both ways.
func TestApplyExitDNS(t *testing.T) {
	apply := func(d *nodeapi.ExitDNS) {
		t.Helper()
		st := cascadeState(t)
		st.DNS = d
		raw, _, err := buildConfig(st, proto.Cert{CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, false, "")
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := executor.ParseWithBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyExitDNS(cfg); err != nil {
			t.Fatal(err)
		}
	}
	defer apply(nil)
	apply(&nodeapi.ExitDNS{Sniff: true, Servers: []string{"https://1.1.1.1/dns-query"}})
	if !tunnel.IsSniffing() || resolver.DefaultResolver == nil || resolver.DirectHostResolver == nil {
		t.Fatalf("on: sniffing %v, resolver %v", tunnel.IsSniffing(), resolver.DefaultResolver)
	}
	apply(nil)
	if tunnel.IsSniffing() || resolver.DefaultResolver != nil || resolver.DirectHostResolver != nil {
		t.Fatalf("off: sniffing %v, resolver %v", tunnel.IsSniffing(), resolver.DefaultResolver)
	}
}
