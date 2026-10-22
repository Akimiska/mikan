package nodesync

import (
	"context"
	"slices"
	"testing"

	"mikan/internal/panel/exitdns"
	"mikan/internal/panel/settings"
)

// The DNS at the exit reaches the nodes on from the start: a panel updated to it turns it
// on for every node. A change of it is a new state; off and the host's resolver leave the
// node as before it.
func TestExitDNSInState(t *testing.T) {
	s, _, st, _, _ := setup(t)
	ctx := context.Background()
	on, err := s.desired(ctx)
	if err != nil || on.DNS == nil || !on.DNS.Sniff || len(on.DNS.Servers) != 0 {
		t.Fatalf("on by default with the host's resolver: %+v %v", on.DNS, err)
	}
	set := func(c exitdns.Config) {
		t.Helper()
		if err := settings.Set(ctx, settings.New(st.Q), exitdns.KeyConfig, c); err != nil {
			t.Fatal(err)
		}
		s.m.SlotsChanged() // as the API does after saving
	}
	set(exitdns.Config{Sniff: true, Resolver: exitdns.ResolverGoogle, Servers: []string{"tls://dns.example"}})
	google, err := s.desired(ctx)
	if err != nil || google.DNS == nil || !slices.Equal(google.DNS.Servers, exitdns.Presets[exitdns.ResolverGoogle]) {
		t.Fatalf("google: %+v %v", google.DNS, err)
	}
	if stateKey(on) == stateKey(google) {
		t.Fatal("the resolver does not change the state key")
	}
	set(exitdns.Config{Resolver: exitdns.ResolverHost, Servers: []string{}})
	off, err := s.desired(ctx)
	if err != nil || off.DNS != nil {
		t.Fatalf("off: %+v %v", off.DNS, err)
	}
}
