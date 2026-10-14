package support

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

type env struct {
	t   *testing.T
	st  *store.Store
	svc *Service
	now time.Time
	adm int64

	mu      sync.Mutex
	notices []Notice
	ended   []int64
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, err := st.Q.CreateAdmin(ctx, db.CreateAdminParams{Username: "admin", PasswordHash: "x", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, now: time.Unix(1_800_000_000, 0), adm: a.ID}
	e.svc = New(st, func() time.Time { return e.now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.svc.SetNotifier(func(_ context.Context, n Notice) {
		e.mu.Lock()
		e.notices = append(e.notices, n)
		e.mu.Unlock()
	})
	e.svc.SetEndedHook(func(id int64) {
		e.mu.Lock()
		e.ended = append(e.ended, id)
		e.mu.Unlock()
	})
	return e
}

func (e *env) create(level string, targets ...string) (Created, error) {
	return e.svc.Create(context.Background(), CreateInput{AdminID: e.adm, Hours: 2, Level: level, Targets: targets, IP: "203.0.113.1"})
}

func TestTokenShape(t *testing.T) {
	tok := NewToken()
	if !ValidToken(tok) || len(tok) != 43 || NewToken() == tok {
		t.Fatalf("token %q", tok)
	}
	if HashToken(tok) == tok || len(HashToken(tok)) != 64 {
		t.Fatalf("hash %q", HashToken(tok))
	}
	for _, bad := range []string{"", "ms_", "mk_" + strings.Repeat("a", 40), "ms_" + strings.Repeat("a", 39), "ms_" + strings.Repeat("a", 41),
		"ms_" + strings.Repeat("a", 39) + "-", "ms_" + strings.Repeat("a", 39) + "é", " ms_" + strings.Repeat("a", 40), "ms_" + strings.Repeat("a", 39) + "\n"} {
		if ValidToken(bad) {
			t.Errorf("%q is taken for a token", bad)
		}
	}
}

func TestTerminalAllowedNeedsLevelAndTarget(t *testing.T) {
	g := db.SupportGrant{Level: LevelTerminal, TerminalTargets: []string{"panel", "node:2"}}
	if !TerminalAllowed(g, "panel") || !TerminalAllowed(g, "node:2") || TerminalAllowed(g, "node:3") || TerminalAllowed(g, "") || TerminalAllowed(g, "PANEL") {
		t.Fatal("terminal targets")
	}
	g.Level = LevelChange
	if TerminalAllowed(g, "panel") {
		t.Fatal("a change grant opens a terminal")
	}
}

// One grant is live; a new one ends the old one (once, in the journal, with its operator
// told when it had been there) and what held something open for it lets go.
func TestOneLiveGrant(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	first, err := e.create(LevelView)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, first.Token); err != nil {
		t.Fatalf("first: %v", err)
	}
	g, _ := e.svc.Authenticate(ctx, first.Token)
	e.svc.Used(ctx, g, "198.51.100.5", "api")
	second, err := e.create(LevelChange)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Authenticate(ctx, first.Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("the replaced grant still opens: %v", err)
	}
	if _, err := e.svc.Authenticate(ctx, second.Token); err != nil {
		t.Fatalf("second: %v", err)
	}
	live, err := e.st.Q.LiveSupportGrants(ctx, e.now.Unix())
	if err != nil || len(live) != 1 || live[0].ID != second.Grant.ID {
		t.Fatalf("live grants: %+v %v", live, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.ended) != 1 || e.ended[0] != first.Grant.ID {
		t.Fatalf("ended hook: %v", e.ended)
	}
	// Two notices: the operator came in, then the access ended because it was replaced.
	if len(e.notices) != 2 || e.notices[0].Leave || !e.notices[1].Leave || e.notices[1].Reason != "replaced" {
		t.Fatalf("notices: %+v", e.notices)
	}
}

func TestExpiryIsNotedOnce(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := e.create(LevelView)
	if err != nil {
		t.Fatal(err)
	}
	g, _ := e.svc.Authenticate(ctx, c.Token)
	if err := e.svc.Enter(ctx, g, "198.51.100.5"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Enter(ctx, g, "198.51.100.5"); !errors.Is(err, ErrUsed) {
		t.Fatalf("second entry: %v", err)
	}
	e.now = e.now.Add(2*time.Hour + time.Second)
	if _, err := e.svc.Authenticate(ctx, c.Token); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired: %v", err)
	}
	for range 3 {
		if err := e.svc.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	leaves := 0
	for _, n := range e.notices {
		if n.Leave && n.Reason == "expired" {
			leaves++
		}
	}
	if leaves != 1 || len(e.ended) != 1 {
		t.Fatalf("expiry told %d times, hook %d times", leaves, len(e.ended))
	}
	// Revoking what already ended is a refusal, not a second end.
	if _, err := e.svc.Revoke(ctx, c.Grant.ID, e.adm, ""); !errors.Is(err, ErrNotLive) {
		t.Fatalf("revoke after expiry: %v", err)
	}
	// The link of an expired grant enters nothing.
	if err := e.svc.Enter(ctx, g, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("enter after expiry: %v", err)
	}
}

func TestTargetsAreChecked(t *testing.T) {
	e := setup(t)
	var node int64
	if err := e.st.DB.QueryRow("INSERT INTO nodes(name,address,created_at,updated_at) VALUES('n','198.51.100.7:1',1,1) RETURNING id").Scan(&node); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		level   string
		targets []string
		code    string
	}{
		"terminal with none":   {LevelTerminal, nil, "terminal_targets_required"},
		"view with targets":    {LevelView, []string{"panel"}, "terminal_targets_not_allowed"},
		"change with targets":  {LevelChange, []string{"panel"}, "terminal_targets_not_allowed"},
		"unknown node":         {LevelTerminal, []string{"node:99999"}, "terminal_targets_invalid"},
		"junk":                 {LevelTerminal, []string{"panel; rm -rf /"}, "terminal_targets_invalid"},
		"not canonical":        {LevelTerminal, []string{"node:" + "0" + NodeTarget(node)[5:]}, "terminal_targets_invalid"},
		"too many":             {LevelTerminal, make([]string, MaxTargets+1), "terminal_targets_invalid"},
		"unknown level":        {"root", nil, "support_level"},
		"a node of some other": {LevelTerminal, []string{"node:-1"}, "terminal_targets_invalid"},
	} {
		_, err := e.create(c.level, c.targets...)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Code != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	if _, err := e.svc.Create(context.Background(), CreateInput{AdminID: e.adm, Hours: 5, Level: LevelView}); err == nil {
		t.Error("5 hours accepted")
	}
	if _, err := e.svc.Create(context.Background(), CreateInput{AdminID: e.adm, Hours: 2, Level: LevelView, Note: strings.Repeat("я", MaxNote+1)}); err == nil {
		t.Error("a long note accepted")
	}
	var n int
	if err := e.st.DB.QueryRow("SELECT count(*) FROM support_grants").Scan(&n); err != nil || n != 0 {
		t.Fatalf("grants made by refused requests: %d %v", n, err)
	}
	c, err := e.create(LevelTerminal, "panel", NodeTarget(node), "panel")
	if err != nil || len(c.Grant.TerminalTargets) != 2 {
		t.Fatalf("ok: %v %+v", err, c.Grant)
	}
}
