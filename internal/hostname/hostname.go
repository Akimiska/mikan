// Package hostname checks the addresses an admin types for a server: an IP, or a DNS name
// clients resolve and a certificate is issued for. The installer keeps the same rule for
// a name (valid_domain in installer/src/net.rs); testdata/hosts.txt holds the cases both
// test suites check.
package hostname

import (
	"net"
	"strings"
)

// Name accepts a DNS name: at most 253 characters, two labels or more of ASCII letters,
// digits and inner hyphens, at most 63 each, the last one not all digits. An IP address
// is not a name.
func Name(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil {
		return false
	}
	labels := strings.Split(s, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			if c := l[i]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return strings.ContainsFunc(labels[len(labels)-1], func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' })
}

// Valid accepts an IP address or a Name.
func Valid(s string) bool { return net.ParseIP(s) != nil || Name(s) }
