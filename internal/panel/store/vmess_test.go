package store

import (
	"context"
	"testing"
)

// Migration 0034 turns the VMess inbounds into VLESS ones, the nodes' core has no VMess:
// only the type changes, the rest of the template and every other inbound stay as they
// were.
func TestVMessInboundsBecomeVLESS(t *testing.T) {
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
	if _, err := p.DownTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	const vmess = "type: vmess\nws-path: /vmess\nmikan:\n    tls: node\n"
	const trojan = "type: trojan\nws-path: /type: vmess\nmikan:\n    tls: node\n"
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO inbounds(id,name,preset,port,settings,created_at,updated_at,config) VALUES
	 (71,'old','custom','2083','{}',1,1,$1), (72,'other','custom','2087','{}',1,1,$2)`, vmess, trojan); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 34); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]string{71: "type: vless\nws-path: /vmess\nmikan:\n    tls: node\n", 72: trojan} {
		var got string
		if err := s.DB.QueryRowContext(ctx, "SELECT config FROM inbounds WHERE id = $1", id).Scan(&got); err != nil || got != want {
			t.Fatalf("inbound %d: %q (%v), want %q", id, got, err, want)
		}
	}
}
