package node

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"mikan/internal/nodeapi"
)

func TestSiteOf(t *testing.T) {
	for _, c := range []struct {
		host string
		addr string
		want string
	}{
		{"rr3---sn-abc.googlevideo.com", "", "googlevideo.com"},
		{"WWW.Example.CO.UK.", "", "example.co.uk"},
		{"", "203.0.113.9", "203.0.113.9"},
		{"203.0.113.9", "203.0.113.9", "203.0.113.9"},
		{"", "", otherSite},
		{"localhost", "", "localhost"},
	} {
		var a netip.Addr
		if c.addr != "" {
			a = netip.MustParseAddr(c.addr)
		}
		if got := siteOf(c.host, a); got != c.want {
			t.Errorf("siteOf(%q, %q) = %q, want %q", c.host, c.addr, got, c.want)
		}
	}
}

// With the detail on a slot's bytes go into the batch by site, bounded; off, nothing is
// summed and what was is dropped.
func TestSitesInTheBatch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := NewRegistry("e1", 0, time.Minute, func() time.Time { return now }, &fakeCore{})
	r.SetSlots([]nodeapi.Slot{{Name: "s1", UUID: "u1"}})
	s := r.byName["s1"]
	r.countSite(s, "example.com", 10, 20)
	if c := r.Counters(); len(c.Sites) != 0 {
		t.Fatalf("summed while off: %+v", c.Sites)
	}
	r.SetDetail(true)
	r.countSite(s, "example.com", 10, 20)
	r.countSite(s, "example.com", 1, 2)
	for i := range maxSites + 5 {
		r.countSite(s, "site"+strconv.Itoa(i)+".com", 1, 0)
	}
	c := r.Counters()
	got := c.Sites["s1"]
	if got["example.com"] != (nodeapi.Traffic{Up: 11, Down: 22}) || len(got) != maxSites+1 || got[otherSite].Up < 6 {
		t.Fatalf("sites: %d entries, example %+v, other %+v", len(got), got["example.com"], got[otherSite])
	}
	if c.Idle {
		t.Fatal("a batch of sites alone is a batch")
	}
	if !r.Ack(c.Epoch, c.Seq) {
		t.Fatal("ack")
	}
	r.countSite(s, "example.com", 5, 5)
	r.SetDetail(false)
	if c := r.Counters(); len(c.Sites) != 0 {
		t.Fatalf("kept after off: %+v", c.Sites)
	}
}
