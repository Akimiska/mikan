package nodeapi

import "testing"

func TestCanonDNSServer(t *testing.T) {
	good := map[string]string{
		"1.1.1.1":                              "1.1.1.1",
		" 8.8.8.8:53 ":                         "8.8.8.8:53",
		"9.9.9.9:05353":                        "9.9.9.9:5353",
		"2606:4700::1111":                      "[2606:4700::1111]",
		"[2606:4700::1111]:53":                 "[2606:4700::1111]:53",
		"::ffff:1.1.1.1":                       "1.1.1.1",
		"udp://1.1.1.1":                        "udp://1.1.1.1",
		"TCP://1.1.1.1:53":                     "tcp://1.1.1.1:53",
		"tls://dns.google":                     "tls://dns.google",
		"tls://Dns.Google:853":                 "tls://dns.google:853",
		"https://1.1.1.1":                      "https://1.1.1.1/dns-query",
		"https://1.1.1.1/":                     "https://1.1.1.1/dns-query",
		"https://cloudflare-dns.com/dns-query": "https://cloudflare-dns.com/dns-query",
		"https://dns.adguard-dns.com/resolve":  "https://dns.adguard-dns.com/resolve",
		"https://[2606:4700::1111]/dns-query":  "https://[2606:4700::1111]/dns-query",
		"quic://dns.adguard-dns.com":           "quic://dns.adguard-dns.com",
	}
	for in, want := range good {
		if got, ok := CanonDNSServer(in); !ok || got != want {
			t.Errorf("CanonDNSServer(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{
		"", "dns.google", "dns.google:53", // plain DNS by name
		"1.1.1.1:0", "1.1.1.1:65536", "1.1.1.1:+53", "1.1.1.1:x",
		"https://1.1.1.1/dns-query#PROXY", "https://1.1.1.1#DIRECT", "tls://1.1.1.1#ecs=1.2.3.0/24",
		"https://user:pass@1.1.1.1/dns-query", "https://1.1.1.1/dns-query?x=1", "https://1.1.1.1/dns-query?",
		"tls://dns.google/path", "dhcp://eth0", "system", "system://", "rcode://success", "http://1.1.1.1/dns-query",
		"tls://*.dns.google", "tls://.dns.google", "tls://dns_google", "tls://", "https:///dns-query",
		"fe80::1%eth0", "[fe80::1%25eth0]:53", "1.1.1.1 8.8.8.8", "tls://[dns.google]",
	} {
		if got, ok := CanonDNSServer(in); ok {
			t.Errorf("CanonDNSServer(%q) = %q, want refused", in, got)
		}
	}
}
