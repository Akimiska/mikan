package store

import (
	"context"
	"testing"
)

// Migration 0043 turns a REALITY template's own max-time-difference from mihomo's
// microseconds into the milliseconds Citrus reads, 0 (no check) stays 0, and the templates
// without one stay as they were. 0047 brings back the 0 that 0043 of rc.11 made 1 ms.
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
	const off = "type: vless\nreality-config:\n    dest: b.com:443\n    max-time-difference: 0\n"
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO inbounds(id,name,preset,port,settings,created_at,updated_at,config) VALUES
	 (81,'own','custom','2083','{}',1,1,$1), (82,'tiny','custom','2087','{}',1,1,$2), (83,'none','custom','2096','{}',1,1,$3),
	 (84,'off','custom','2097','{}',1,1,$4)`, own, tiny, none, off); err != nil {
		t.Fatal(err)
	}
	check := func(want map[int]string) {
		t.Helper()
		for id, want := range want {
			var got string
			if err := s.DB.QueryRowContext(ctx, "SELECT config FROM inbounds WHERE id = $1", id).Scan(&got); err != nil || got != want {
				t.Fatalf("inbound %d: %q (%v), want %q", id, got, err, want)
			}
		}
	}
	if _, err := p.UpTo(ctx, 43); err != nil {
		t.Fatal(err)
	}
	check(map[int]string{
		81: "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    max-time-difference: 60000\n    private-key: k\n",
		82: "type: vless\nreality-config:\n    dest: a.com:443\n    max-time-difference: 1\n",
		83: none,
		84: off,
	})
	if _, err := p.UpTo(ctx, 47); err != nil {
		t.Fatal(err)
	}
	check(map[int]string{
		81: "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    max-time-difference: 60000\n    private-key: k\n",
		82: "type: vless\nreality-config:\n    dest: a.com:443\n    max-time-difference: 0\n",
		84: off,
	})
}
