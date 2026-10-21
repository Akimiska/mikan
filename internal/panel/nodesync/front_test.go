package nodesync

import (
	"context"
	"slices"
	"testing"

	"mikan/internal/proto"
)

// A node's front page reaches it: the state names the page, and with the shared 443 the
// router gives it the ClientHellos with no name; without the shared 443 the state names the
// page all the same, for the inbounds on an HTTP transport.
func TestFrontPageInTheState(t *testing.T) {
	s, _, st, _, _ := setup(t)
	ctx := context.Background()
	q := st.Q
	ins, _ := q.ListInbounds(ctx)
	routed := false
	for _, in := range ins {
		tm, err := proto.Parse(in.Config)
		if err != nil || !proto.Reality(tm) || !proto.CanSNI443(tm) {
			continue
		}
		mk, _ := tm["mikan"].(map[string]any)
		if mk == nil {
			mk = map[string]any{}
		}
		mk["sni443"] = true
		tm["mikan"] = mk
		if _, err := st.DB.ExecContext(ctx, "UPDATE inbounds SET config = $1 WHERE id = $2", proto.Marshal(tm), in.ID); err != nil {
			t.Fatal(err)
		}
		routed = true
		break
	}
	if !routed {
		t.Fatal("no REALITY inbound for the shared 443 seeded")
	}
	node := func(router int, page string) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, "UPDATE nodes SET sni_router = $1, front_page = $2 WHERE id = $3", router, page, s.id); err != nil {
			t.Fatal(err)
		}
	}

	node(1, "cats")
	d, err := s.desired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.FrontPage != "cats" || d.Router == nil || !slices.Contains(d.Router.Stub, "") {
		t.Fatalf("the page with the shared 443: page %q router %+v", d.FrontPage, d.Router)
	}
	node(1, "")
	if d, _ = s.desired(ctx); d.FrontPage != "" || d.Router == nil || len(d.Router.Stub) != 0 {
		t.Fatalf("no page: page %q router %+v", d.FrontPage, d.Router)
	}
	node(0, "cats")
	if d, _ = s.desired(ctx); d.FrontPage != "cats" || d.Router != nil {
		t.Fatalf("the page without the shared 443: page %q router %+v", d.FrontPage, d.Router)
	}
}
