package nodesync

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

// A remote node with the API lock on gets the panel's own addresses (its public address and
// what its domain resolves to) and the networks the admin added; the panel's own node never
// does, and a node with the lock off gets none.
func TestAPIAllowInTheState(t *testing.T) {
	local, _, st, _, _ := setup(t)
	ctx := context.Background()
	remote, _ := addRemote(t, local, st)
	set := settings.New(st.Q)
	if err := settings.Set(ctx, set, settings.KeyPublicHost, "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(ctx, set, settings.KeyDomain, "panel.example"); err != nil {
		t.Fatal(err)
	}
	lookups := 0
	local.m.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		lookups++
		if host != "panel.example" {
			t.Errorf("looked up %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("198.51.100.2"), netip.MustParseAddr("::ffff:198.51.100.1"), netip.MustParseAddr("2001:db8::2")}, nil
	}
	for _, s := range []*Syncer{local, remote} {
		got, err := s.desired(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got.APIAllow != nil {
			t.Fatalf("the lock is off by default: %v", got.APIAllow)
		}
	}
	for _, id := range []int64{local.id, remote.id} {
		if err := st.Q.SetNodeAPILockdown(ctx, db.SetNodeAPILockdownParams{ApiLockdown: 1, ApiAllow: []string{"203.0.113.0/24"}, ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := local.desired(ctx)
	b, _ := remote.desired(ctx)
	if a.APIAllow != nil {
		t.Fatalf("the panel's own node is reached over its socket, and got a lock: %v", a.APIAllow)
	}
	want := []string{"198.51.100.1/32", "198.51.100.2/32", "2001:db8::2/128", "203.0.113.0/24"}
	if !slices.Equal(b.APIAllow, want) {
		t.Fatalf("the remote node's lock: %v, want %v", b.APIAllow, want)
	}
	// The panel's addresses are looked up once, not with every state.
	b, _ = remote.desired(ctx)
	if lookups != 1 || !slices.Equal(b.APIAllow, want) {
		t.Fatalf("%d lookups, %v", lookups, b.APIAllow)
	}
	// A lock with nothing the panel knows stays on: the node adds where the panel came from.
	if err := st.Q.SetNodeAPILockdown(ctx, db.SetNodeAPILockdownParams{ApiLockdown: 1, ApiAllow: []string{}, ID: remote.id}); err != nil {
		t.Fatal(err)
	}
	local.m.addrs, local.m.addrAt = nil, local.m.now()
	if b, _ = remote.desired(ctx); b.APIAllow == nil || len(b.APIAllow) != 0 {
		t.Fatalf("an empty lock must stay on: %#v", b.APIAllow)
	}
	// The key moves with the lock, so turning it on or off reaches the node.
	off := b
	off.APIAllow = nil
	if stateKey(off) == stateKey(b) {
		t.Fatal("the state key does not see the lock")
	}
}
