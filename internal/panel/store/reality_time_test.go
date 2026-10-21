package store

import (
	"context"
	"testing"
)

// Migration 0043 turns a REALITY template's own max-time-difference from mihomo's
// microseconds into the milliseconds Citrus reads; the templates without one stay as they
// were.
func TestRealityTimeDifferenceInMilliseconds(t *testing.T) {
	ctx := context.Background()
	s, err := OpenTest(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := postgresProvider(ctx, s.DB, postgresFS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 42); err != nil {
		t.Fatal(err)
	}
	const own = "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    max-time-difference: 60000000\n    private-key: k\n"
	const tiny = "type: vless\nreality-config:\n    dest: a.com:443\n    max-time-difference: 300\n"
	const none = "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    private-key: k\n"
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO inbounds(id,name,preset,port,settings,created_at,updated_at,config) VALUES
	 (81,'own','custom','2083','{}',1,1,$1), (82,'tiny','custom','2087','{}',1,1,$2), (83,'none','custom','2096','{}',1,1,$3)`, own, tiny, none); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 43); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]string{
		81: "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    max-time-difference: 60000\n    private-key: k\n",
		82: "type: vless\nreality-config:\n    dest: a.com:443\n    max-time-difference: 1\n",
		83: none,
	} {
		var got string
		if err := s.DB.QueryRowContext(ctx, "SELECT config FROM inbounds WHERE id = $1", id).Scan(&got); err != nil || got != want {
			t.Fatalf("inbound %d: %q (%v), want %q", id, got, err, want)
		}
	}
}
