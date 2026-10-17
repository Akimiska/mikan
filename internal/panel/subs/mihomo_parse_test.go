package subs

import (
	"testing"

	"github.com/metacubex/mihomo/config"
)

// The profile is what an app loads: mihomo's own parser must take it whole.
func TestMihomoParsesTheProfile(t *testing.T) {
	prof := profile(t, "")
	for _, r := range []Routing{RoutingAll, RoutingRUDirect} {
		raw, err := Mihomo(prof, Groups{Main: "🚀 Мой VPN", Auto: "on"}, r)
		if err != nil {
			t.Fatal(err)
		}
		rc, err := config.UnmarshalRawConfig(raw)
		if err != nil {
			t.Fatalf("%s: %v\n%s", r, err, raw)
		}
		if len(rc.Proxy) == 0 || len(rc.ProxyGroup) == 0 || len(rc.Rule) == 0 {
			t.Fatalf("%s: an empty section:\n%s", r, raw)
		}
		if g := rc.ProxyGroup[1]["name"]; g != "on" {
			t.Fatalf("a group named %q came back as %v", "on", g)
		}
	}
}
