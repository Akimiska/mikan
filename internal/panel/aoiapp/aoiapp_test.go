package aoiapp

import (
	"testing"
	"time"
)

func TestNormalizeToken(t *testing.T) {
	for in, want := range map[string]string{
		"https://sub.aoimusic.ru/sub/AbCdEf123456789012345678": "AbCdEf123456789012345678",
		"https://vpn.aoimusic.ru/vpn-configs/abcdef12.sub":     "abcdef12",
		"abcdef?x=1":   "abcdef",
		"https://x/y/": "",
		"abc":          "",
		" tok_en-123 ": "tok_en-123",
		"bad token!":   "",
	} {
		if got := normalizeToken(in); got != want {
			t.Errorf("normalizeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMonthsLabel(t *testing.T) {
	for n, want := range map[int64]string{1: "1 месяц", 3: "3 месяца", 6: "6 месяцев", 12: "12 месяцев", 21: "21 месяц"} {
		if got := monthsLabel(n); got != want {
			t.Errorf("monthsLabel(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := limiter{max: 2, window: time.Minute}
	now := time.Unix(1000, 0)
	if !l.allow("a", now) || !l.allow("a", now) || l.allow("a", now) {
		t.Fatal("third hit within the window must be refused")
	}
	if !l.allow("b", now) {
		t.Fatal("another client is not limited")
	}
	if !l.allow("a", now.Add(time.Minute)) {
		t.Fatal("the window passed")
	}
}
