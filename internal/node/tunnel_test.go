package node

import (
	"net"
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

type namedConn struct {
	net.Conn
	name string
}

func (c namedConn) UserName() string { return c.name }

// mihomo 1.19.31 leaves metadata.InUser empty for Mieru; the connection knows the user.
func TestUserOf(t *testing.T) {
	if got := userOf(namedConn{name: "s1"}, &C.Metadata{Type: C.MIERU}); got != "s1" {
		t.Fatalf("mieru: %q", got)
	}
	if got := userOf(namedConn{name: "x"}, &C.Metadata{Type: C.VLESS, InUser: "s2"}); got != "s2" {
		t.Fatalf("the listener's own answer wins: %q", got)
	}
	if got := userOf(namedConn{name: "x"}, &C.Metadata{Type: C.VLESS}); got != "" {
		t.Fatalf("only Mieru falls back to the connection: %q", got)
	}
}
