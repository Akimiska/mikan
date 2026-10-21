package domain

import (
	"errors"
	"slices"
	"strconv"
	"testing"
)

func TestNormalizeAPIAllow(t *testing.T) {
	got, err := NormalizeAPIAllow([]string{" 203.0.113.9 ", "198.51.100.7/24", "::ffff:203.0.113.9", "2001:db8::1/64", "", "203.0.113.9/32", "::ffff:192.0.2.0/120"})
	want := []string{"192.0.2.0/24", "198.51.100.0/24", "2001:db8::/64", "203.0.113.9/32"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	if got, err := NormalizeAPIAllow(nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nothing: %#v, %v", got, err)
	}
	for _, bad := range []string{"nope", "0.0.0.0/0", "::/0", "203.0.113.5/33", "fe80::1%eth0", "203.0.113.05", "example.com"} {
		var e *APIAllowError
		if _, err := NormalizeAPIAllow([]string{bad}); !errors.As(err, &e) || e.Value != bad {
			t.Errorf("%q: %v", bad, err)
		}
	}
	many := make([]string, MaxAPIAllow+1)
	for i := range many {
		many[i] = "203.0.113." + strconv.Itoa(i+1)
	}
	if _, err := NormalizeAPIAllow(many); !errors.Is(err, ErrManyAPIAllow) {
		t.Fatalf("%d networks: %v", len(many), err)
	}
	// The same network written twice counts once.
	if _, err := NormalizeAPIAllow(append(many[:MaxAPIAllow], many[0])); err != nil {
		t.Fatal(err)
	}
}
