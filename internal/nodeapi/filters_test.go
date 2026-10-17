package nodeapi

import "testing"

// The filters' entries as the panel keeps them and the node writes them into its rules.
func TestCanonFilters(t *testing.T) {
	for in, want := range map[string]string{
		"465": "465", " 465 ": "465", "0465": "465", "1000-2000": "1000-2000", "1000 - 2000": "1000-2000",
		"6881-6881": "6881", "1-65535": "1-65535",
		"0": "", "65536": "", "+465": "", "-1": "", "1-+5": "", "9-8": "", "1-": "", "-": "", "": "", "4 65": "",
		"465,587": "", "465/587": "", "１２３": "", "999999999999999999999": "",
	} {
		got, ok := CanonPorts(in)
		if ok != (want != "") || got != want {
			t.Errorf("CanonPorts(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for in, want := range map[string]string{
		"203.0.113.7/24": "203.0.113.0/24", "198.51.100.7": "198.51.100.7/32", "2001:DB8::1/32": "2001:db8::/32",
		"::ffff:198.51.100.7": "198.51.100.7/32", "::ffff:203.0.113.0/120": "203.0.113.0/24", "::ffff:0:0/96": "0.0.0.0/0",
		"::/0": "::/0", " 10.0.0.0/8 ": "10.0.0.0/8",
		"300.1.1.1": "", "10.0.0.0/33": "", "fe80::1%eth0": "", "fe80::1%eth0/64": "", "example.com": "", "": "",
	} {
		p, ok := CanonNetwork(in)
		if ok != (want != "") || (ok && p.String() != want) {
			t.Errorf("CanonNetwork(%q) = %v %v, want %q", in, p, ok, want)
		}
	}
	for in, want := range map[string]string{
		"Example.COM": "example.com", "*.mail.example": "mail.example", ".spam.example.": "spam.example", "xn--d1acufc.xn--p1ai": "xn--d1acufc.xn--p1ai",
		"localhost": "localhost", "Яндекс.РФ": "xn--d1acpjx3f.xn--p1ai", "*.пример.рф": "xn--e1afmkfd.xn--p1ai",
		"": "", "*": "", ".": "", "a..b": "", "-a.com": "", "a-.com": "", "bad,REJECT": "", "a b.com": "", "a_b.com": "",
		"203.0.113.7": "", "bad)": "", "я я.рф": "", "\xff.com": "", "ÿ.com": "xn--wda.com",
	} {
		got, ok := CanonDomain(in)
		if ok != (want != "") || got != want {
			t.Errorf("CanonDomain(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
}
