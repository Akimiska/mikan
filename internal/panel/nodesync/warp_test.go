package nodesync

import (
	"context"
	"encoding/base64"
	"testing"

	"mikan/internal/panel/store/db"
)

// A node gets WARP only when it has it on, with its own inbounds set to WARP and the
// admin's lists; another node's WARP inbounds and other nodes stay direct.
func TestWarpInDesiredState(t *testing.T) {
	local, _, st, _, _ := setup(t)
	ctx := context.Background()
	remote, _ := addRemote(t, local, st)
	ins, _ := st.Q.ListInbounds(ctx)
	var mine, theirs db.Inbound
	for _, in := range ins {
		switch {
		case in.NodeID == local.id && mine.ID == 0:
			mine = in
		case in.NodeID == remote.id && theirs.ID == 0:
			theirs = in
		}
	}
	for _, in := range []db.Inbound{mine, theirs} {
		if err := st.Q.SetInboundOutbound(ctx, db.SetInboundOutboundParams{Outbound: "warp", ID: in.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := local.desired(ctx); d.Warp != nil {
		t.Fatal("WARP without an account")
	}
	if err := st.Q.SaveNodeWarp(ctx, db.SaveNodeWarpParams{NodeID: local.id, Source: "import", PrivateKey: "priv", PeerPublicKey: "pub",
		Endpoint: "162.159.192.1:2408", Ipv4: "172.16.0.2", Reserved: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}), Mtu: 1280,
		Routes: `["openai.com","104.16.0.0/13"]`, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	d, err := local.desired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := d.Warp
	if w == nil || len(w.Inbounds) != 1 || w.Inbounds[0] != mine.Name || len(w.Reserved) != 3 ||
		len(w.Domains) != 1 || w.Domains[0] != "openai.com" || len(w.CIDRs) != 1 || w.CIDRs[0] != "104.16.0.0/13" {
		t.Fatalf("warp: %+v", w)
	}
	if r, _ := remote.desired(ctx); r.Warp != nil {
		t.Fatal("a node without WARP got WARP")
	}
	key := stateKey(d)
	if err := st.Q.SetNodeWarpOptions(ctx, db.SetNodeWarpOptionsParams{Enabled: 0, Routes: "[]", UpdatedAt: 2, NodeID: local.id}); err != nil {
		t.Fatal(err)
	}
	d, _ = local.desired(ctx)
	if d.Warp != nil || stateKey(d) == key {
		t.Fatal("turning WARP off must reach the node")
	}
}
