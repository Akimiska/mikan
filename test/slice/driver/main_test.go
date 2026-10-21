package main

import (
	"strings"
	"testing"

	"mikan/internal/panel/presets"
	"mikan/internal/proto"
)

// remoteFlag starts the proxy names of the remote node's inbounds in the subscription.
const remoteFlag = "🇺🇸 "

// protos is written by hand beside what the catalog and the templates already say, and it
// drifted once: Shadowsocks-2022 got a key per user and is charged, the table still called it
// shared. A row is a preset by its proxy name; whether it is shared is what the template of
// that preset says; and the counts the phases expect of a node follow from the rows.
func TestProtosMatchThePresets(t *testing.T) {
	var local, clash int
	rows := map[string]bool{} // proxy names of the panel's node
	for _, pr := range protos {
		name, remote := strings.CutPrefix(pr.proxy, remoteFlag)
		var p presets.Info
		for _, c := range presets.All {
			if c.SubName == name {
				p = c
			}
		}
		if p.ID == "" {
			t.Errorf("%s: no preset has the proxy name %q", pr.name, name)
			continue
		}
		if remote && !p.Default {
			t.Errorf("%s: a new node gets the default inbounds only, and %s is not one", pr.name, p.ID)
		}
		if !remote {
			local++
			rows[name] = true
			if p.Apps == "mihomo" {
				clash++
			}
		}
		src := customTemplate
		if p.ID != presets.Custom {
			var err error
			if src, err = presets.NewConfig(p.ID, ""); err != nil {
				t.Errorf("%s: %v", pr.name, err)
				continue
			}
		}
		tpl, err := proto.Parse(src)
		if err == nil && p.ID == presets.Custom {
			// The own template goes through the panel's checks, which a type the core lost fails.
			err = proto.Validate(tpl, proto.Options{})
		}
		if err != nil {
			t.Errorf("%s: %v", pr.name, err)
			continue
		}
		if got := proto.Shared(tpl); got != pr.shared {
			t.Errorf("%s: the table says shared=%v, the template of %s says %v", pr.name, pr.shared, p.ID, got)
		}
	}
	if local != localProtos {
		t.Errorf("the table has %d inbounds of the panel's node, localProtos says %d", local, localProtos)
	}
	if clash != clashOnly {
		t.Errorf("the table has %d inbounds only Clash apps get, clashOnly says %d", clash, clashOnly)
	}
	var defaults int
	for _, p := range presets.All {
		if !p.Default {
			continue
		}
		defaults++
		if !rows[p.SubName] {
			t.Errorf("%s is made on a fresh install and the table has no row for it", p.ID)
		}
	}
	if defaults != remoteInbounds {
		t.Errorf("a new node gets %d default inbounds, remoteInbounds says %d", defaults, remoteInbounds)
	}
	for _, d := range devProtos {
		var found bool
		for _, pr := range protos {
			found = found || pr.name == d.name && pr.proxy == d.proxy
		}
		if !found {
			t.Errorf("the device is checked through %s/%q, which the table has no row for", d.name, d.proxy)
		}
	}
}
