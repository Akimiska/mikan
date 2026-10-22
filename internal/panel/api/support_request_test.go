package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"mikan/internal/panel/auth"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
	"mikan/internal/panel/support"
)

// A request outside huma (a WebSocket's upgrade) finds its support grant from the cookie
// session or the ms_ token, and never from the admin's own session or an API key's token.
func TestSupportFromRequest(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	admin, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	clock := func() time.Time { return now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := support.New(st, clock, log)
	sessions := auth.NewSessions(st.Q, clock, log)
	h := &handlers{d: Deps{Store: st, Sessions: sessions, Support: svc, Now: clock, Log: log,
		IPLimit: auth.NewLimiter(10, 10*time.Minute, 15*time.Minute, time.Hour)}}

	node, err := st.Q.CreateNode(ctx, db.CreateNodeParams{Name: "n", Address: "198.51.100.7:1", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.Create(ctx, support.CreateInput{AdminID: admin.ID, Hours: 2, Level: support.LevelTerminal, Targets: []string{"panel", support.NodeTarget(node.ID)}})
	if err != nil {
		t.Fatal(err)
	}
	cookie, _, err := sessions.CreateSupport(ctx, admin.ID, c.Grant.ID, c.Grant.ExpiresAt, "203.0.113.9", "ua")
	if err != nil {
		t.Fatal(err)
	}
	own, _, err := sessions.Create(ctx, admin.ID, "203.0.113.9", "ua")
	if err != nil {
		t.Fatal(err)
	}
	req := func(mod func(*http.Request)) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.9:4000"
		mod(r)
		return r
	}
	withCookie := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: v}) }
	}
	withBearer := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+v) }
	}
	check := func(name string, r *http.Request, want bool) {
		t.Helper()
		g, ok, err := h.supportFromRequest(r)
		if err != nil || ok != want || ok && g.ID != c.Grant.ID {
			t.Errorf("%s: %+v %v %v, want %v", name, g, ok, err, want)
		}
	}
	check("nothing", req(func(*http.Request) {}), false)
	check("support cookie", req(withCookie(cookie)), true)
	check("the admin's own cookie", req(withCookie(own)), false)
	check("junk cookie", req(withCookie("nope")), false)
	check("support token", req(withBearer(c.Token)), true)
	check("a key's token", req(withBearer("mk_"+c.Token[3:])), false)
	check("wrong token", req(withBearer(support.NewToken())), false)

	g, _, _ := h.supportFromRequest(req(withBearer(c.Token)))
	ctxTerm := withSupportGrant(ctx, g)
	if !supportTerminalAllowed(ctxTerm, "panel") || !supportTerminalAllowed(ctxTerm, support.NodeTarget(node.ID)) || supportTerminalAllowed(ctxTerm, "node:9999") {
		t.Error("terminal targets of the grant")
	}
	if actor, ok := supportActor(ctxTerm); !ok || actor != "support:"+idStr(c.Grant.ID) {
		t.Errorf("actor %q %v", actor, ok)
	}

	// Revoked: both ways stop at once.
	if _, err := svc.Revoke(ctx, c.Grant.ID, admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	check("revoked cookie", req(withCookie(cookie)), false)
	check("revoked token", req(withBearer(c.Token)), false)
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }
