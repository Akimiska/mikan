package node

import "testing"

func TestConnLinesStayOutOfLogs(t *testing.T) {
	for _, msg := range []string{
		"[TCP] dial DIRECT (match Match/) 203.0.113.9:57314 --> [2001:db8::a]:443 error: connect: network is unreachable",
		"[UDP] dial DIRECT (match Match/) 203.0.113.9:4000 --> example.com:443 error: dns resolve failed",
	} {
		if !connLine(msg) {
			t.Errorf("per-connection line must be dropped: %q", msg)
		}
	}
	for _, msg := range []string{
		"Listener vless-vision listen err: listen tcp :443: bind: address already in use",
		"mikan-sync 00ff",
	} {
		if connLine(msg) {
			t.Errorf("line must be kept: %q", msg)
		}
	}
	if name, reason, ok := parseListenErr("Listener tuic listen err: bind: address already in use"); !ok || name != "tuic" || reason != "bind: address already in use" {
		t.Errorf("parseListenErr = %q %q %v", name, reason, ok)
	}
}
