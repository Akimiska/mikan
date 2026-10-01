package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"mikan/internal/panel/presets"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/proto"
)

func TestAddPreset(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	set := settings.New(st.Q)
	info, _ := presets.Get("trojan_reality")

	in, err := AddPreset(ctx, st, set, 1, "trojan_reality", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if in.Port != info.Port || in.Name != info.Name || in.Config == "" || in.Enabled == 0 {
		t.Fatalf("inbound: %+v", in)
	}
	var busy *PortInUseError
	if _, err := AddPreset(ctx, st, set, 1, "trojan_reality", "", now); !errors.As(err, &busy) || busy.Owner != info.Name {
		t.Fatalf("the preset's port is taken now: %v", err)
	}
	second, err := AddPreset(ctx, st, set, 1, "trojan_reality", "20000", now)
	if err != nil || second.Name != info.Name+"-2" {
		t.Fatalf("second inbound: %+v %v", second, err)
	}
	// TCP and UDP listeners share a port number without conflict.
	if _, err := AddPreset(ctx, st, set, 1, "tuic_v5", "20000", now); err != nil {
		t.Fatalf("udp next to tcp: %v", err)
	}
	for _, c := range []struct {
		id, port string
		want     error
	}{
		{"nope", "", ErrUnknownPreset},
		{presets.Custom, "", ErrUnknownPreset},
		{"anytls", "70000", ErrBadPort},
		{"anytls", "2083-2000", ErrBadPort},
	} {
		if _, err := AddPreset(ctx, st, set, 1, c.id, c.port, now); !errors.Is(err, c.want) {
			t.Errorf("%s %q: got %v, want %v", c.id, c.port, err, c.want)
		}
	}
}

// Seeded node 1: vless-xhttp 443/tcp, hysteria2 443/udp, tuic 8443/udp, vless-vision 8443/tcp.
func TestSetInboundPort(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()

	later := now.Add(time.Hour)
	prev, next, err := SetInboundPort(ctx, st, 1, "vless-xhttp", "2443", later)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Port != "443" || next.Port != "2443" || next.ID != prev.ID || next.Config != prev.Config || next.Enabled != prev.Enabled || next.UpdatedAt != later.Unix() {
		t.Fatalf("moved: %+v -> %+v", prev, next)
	}
	// TCP and UDP listeners share a port number without conflict.
	if _, next, err := SetInboundPort(ctx, st, 1, "hysteria2", "2443", later); err != nil || next.Port != "2443" {
		t.Fatalf("udp next to tcp: %+v %v", next, err)
	}
	var busy *PortInUseError
	if _, _, err := SetInboundPort(ctx, st, 1, "tuic", "2443", later); !errors.As(err, &busy) || busy.Owner != "hysteria2" {
		t.Fatalf("udp 2443 is taken by hysteria2: %v", err)
	}
	if _, _, err := SetInboundPort(ctx, st, 1, "vless-vision", "2443", later); !errors.As(err, &busy) || busy.Owner != "vless-xhttp" {
		t.Fatalf("tcp 2443 is taken by vless-xhttp: %v", err)
	}

	// Ports and names are per node: node 2 has its own vless-xhttp.
	n, err := st.Q.CreateNode(ctx, db.CreateNodeParams{Name: "🇺🇸 США", Address: "203.0.113.7:25305", PublicHost: "203.0.113.7", CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	config, err := presets.NewConfig("vless_reality_xhttp", "")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := st.Q.CreateInbound(ctx, db.CreateInboundParams{NodeID: n.ID, Name: "vless-xhttp", Preset: "vless_reality_xhttp", Port: "443", Config: config,
		CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, next, err := SetInboundPort(ctx, st, n.ID, "vless-xhttp", "2443", later); err != nil || next.ID != remote.ID || next.Port != "2443" {
		t.Fatalf("node 2 inbound: %+v %v", next, err)
	}
	local, err := st.Q.GetInbound(ctx, prev.ID)
	if err != nil || local.Port != "2443" || local.NodeID != 1 {
		t.Fatalf("node 1 inbound must stay: %+v %v", local, err)
	}

	for _, c := range []struct {
		node       int64
		name, port string
		want       error
	}{
		{1, "nope", "3000", ErrUnknownInbound},
		{9, "vless-xhttp", "3000", ErrUnknownNode},
		{1, "vless-xhttp", "0", ErrBadPort},
		{1, "vless-xhttp", "70000", ErrBadPort},
		{1, "vless-xhttp", "", ErrBadPort},
	} {
		if _, _, err := SetInboundPort(ctx, st, c.node, c.name, c.port, later); !errors.Is(err, c.want) {
			t.Errorf("node %d %s %q: got %v, want %v", c.node, c.name, c.port, err, c.want)
		}
	}
}

// The installer and the admin point REALITY inbounds at a site next to the server; the
// panel's own HTTPS (self-steal) is for the panel's own node only.
func TestSetInboundTarget(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	if err := settings.Set(ctx, settings.New(st.Q), settings.KeyPanelPort, 21355); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	prev, next, err := SetInboundTarget(ctx, st, 1, "vless-xhttp", "203.0.113.20:443", "www.example.org", later)
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := proto.Parse(next.Config)
	dest, names := presets.Dest(tpl)
	if dest != "203.0.113.20:443" || len(names) != 1 || names[0] != "www.example.org" || next.UpdatedAt != later.Unix() || next.Port != prev.Port {
		t.Fatalf("retargeted: %s %v %+v", dest, names, next)
	}
	// The keys stay: clients keep working after they refresh the subscription.
	old, _ := proto.Parse(prev.Config)
	if old["reality-config"].(map[string]any)["private-key"] != tpl["reality-config"].(map[string]any)["private-key"] {
		t.Fatal("the REALITY key changed")
	}
	if _, _, err := SetInboundTarget(ctx, st, 1, "vless-vision", "127.0.0.1:21355", "vpn.example.com", later); err != nil {
		t.Fatalf("self-steal on the panel's node: %v", err)
	}

	n, err := st.Q.CreateNode(ctx, db.CreateNodeParams{Name: "🇺🇸 США", Address: "203.0.113.7:25305", PublicHost: "203.0.113.7", CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := presets.NewConfig("trojan_reality", "")
	if _, err := st.Q.CreateInbound(ctx, db.CreateInboundParams{NodeID: n.ID, Name: "trojan", Preset: "trojan_reality", Port: "2087", Config: config,
		CreatedAt: now.Unix(), UpdatedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	var pe *proto.Error
	if _, _, err := SetInboundTarget(ctx, st, n.ID, "trojan", "127.0.0.1:21355", "vpn.example.com", later); !errors.As(err, &pe) {
		t.Fatalf("a remote node cannot borrow the panel's HTTPS: %v", err)
	}
	if _, _, err := SetInboundTarget(ctx, st, 1, "vless-xhttp", "203.0.113.20:443", "", later); !errors.As(err, &pe) || pe.Code != "reality_sni" {
		t.Fatalf("an IP target needs the site's name: %v", err)
	}
	for _, c := range []struct {
		node int64
		name string
		want error
	}{
		{1, "hysteria2", ErrNoReality},
		{1, "nope", ErrUnknownInbound},
		{9, "vless-xhttp", ErrUnknownNode},
	} {
		if _, _, err := SetInboundTarget(ctx, st, c.node, c.name, "www.example.org:443", "", later); !errors.Is(err, c.want) {
			t.Errorf("node %d %s: got %v, want %v", c.node, c.name, err, c.want)
		}
	}
}

// The listen address the admin types: every address, or one IP literal.
func TestParseListen(t *testing.T) {
	for in, want := range map[string]string{"": "", " ": "", "0.0.0.0": "", "::": "", "127.0.0.1": "127.0.0.1", " 10.0.0.5 ": "10.0.0.5",
		"::1": "::1", "2001:DB8::1": "2001:db8::1", "::ffff:127.0.0.1": "127.0.0.1"} {
		if got, err := ParseListen(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"localhost", "127.0.0.1:444", "fe80::1%eth0", "224.0.0.1", "10.0.0.0/8", "0.0.0.0\nlisten: x"} {
		if _, err := ParseListen(bad); !errors.Is(err, ErrBadListen) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
	if ListenPinsPort("") || !ListenPinsPort("127.0.0.1") {
		t.Fatal("only an address of its own pins the port")
	}
}

// One port number per node and network, whatever address the inbounds listen on.
func TestPortTakenOnAnotherAddress(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	st, _, _ := setup(t, &now)
	ctx := context.Background()
	_, moved, err := SetInboundPort(ctx, st, 1, "vless-xhttp", "444", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Q.SetInboundListen(ctx, db.SetInboundListenParams{Listen: "127.0.0.1", ID: moved.ID}); err != nil {
		t.Fatal(err)
	}
	var busy *PortInUseError
	if _, _, err := SetInboundPort(ctx, st, 1, "vless-vision", "444", now); !errors.As(err, &busy) || busy.Owner != "vless-xhttp" {
		t.Fatalf("tcp 444 is taken by vless-xhttp on 127.0.0.1: %v", err)
	}
}
