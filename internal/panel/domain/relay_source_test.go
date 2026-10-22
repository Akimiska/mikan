package domain

import "testing"

// A relay user's name stands for its source node; no other name does.
func TestRelaySource(t *testing.T) {
	if id, ok := RelaySource(RelayUserName(42)); !ok || id != 42 {
		t.Fatalf("relay-42: %d %v", id, ok)
	}
	for _, name := range []string{"", "relay-", "relay-0", "relay--1", "relay-01", "relay-1x", "s1", "relay-2-x"} {
		if _, ok := RelaySource(name); ok {
			t.Fatalf("%q read as a relay user", name)
		}
	}
}
