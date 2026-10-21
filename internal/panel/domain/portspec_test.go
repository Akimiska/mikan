package domain

import "testing"

// A port is "443" or a hopping range "20000-30000": the one grammar of the panel's checks and
// of the links the subscription renders, so a list or a space is no port in either.
func TestParsePortAndFirstPort(t *testing.T) {
	for _, c := range []struct {
		spec   string
		lo, hi int
	}{
		{"443", 443, 443},
		{"1", 1, 1},
		{"65535", 65535, 65535},
		{"20000-30000", 20000, 30000},
		{"1-2", 1, 2},
		{"", 0, 0},
		{"0", 0, 0},
		{"65536", 0, 0},
		{"-5", 0, 0},
		{"abc", 0, 0},
		{"443,8443", 0, 0},
		{"443-", 0, 0},
		{"443-443", 0, 0},
		{"30000-20000", 0, 0},
		{"20000-70000", 0, 0},
		{" 443", 0, 0},
	} {
		lo, hi, ok := ParsePort(c.spec)
		if ok != (c.lo != 0) || lo != c.lo || hi != c.hi {
			t.Errorf("ParsePort(%q) = %d, %d, %v", c.spec, lo, hi, ok)
		}
		if got := FirstPort(c.spec); got != c.lo {
			t.Errorf("FirstPort(%q) = %d, want %d", c.spec, got, c.lo)
		}
	}
}
