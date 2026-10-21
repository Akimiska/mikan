package presets

import (
	"strings"
	"testing"

	"mikan/internal/proto"
)

// A copy shares no secret with the original: every key a preset generates is new, the
// rest of the settings stays.
func TestRekey(t *testing.T) {
	for _, id := range []string{"vless_reality_xhttp", PresetPQ, "hysteria2", "shadowsocks_2022", "sudoku", "snell", PresetTLSWS} {
		src, err := NewConfig(id, "")
		if err != nil {
			t.Fatal(err)
		}
		orig, _ := proto.Parse(src)
		cp, _ := proto.Parse(src)
		if err := Rekey(cp); err != nil {
			t.Fatal(err)
		}
		if err := proto.Validate(cp, proto.Options{}); err != nil {
			t.Fatalf("%s: the copy is broken: %v", id, err)
		}
		changed := false
		for _, k := range []string{"obfs-password", "password", "key", "psk", "decryption"} {
			if orig[k] != nil {
				if orig[k] == cp[k] {
					t.Fatalf("%s: %s kept", id, k)
				}
				changed = true
			}
		}
		if r, ok := orig["reality-config"].(map[string]any); ok {
			c := cp["reality-config"].(map[string]any)
			if r["private-key"] == c["private-key"] || r["dest"] != c["dest"] {
				t.Fatalf("%s: reality %v / %v", id, r, c)
			}
			changed = true
		}
		if id == PresetTLSWS && changed {
			t.Fatal("VLESS TLS has no key of its own to change")
		}
		if id != PresetTLSWS && !changed {
			t.Fatalf("%s: nothing new", id)
		}
	}
}

// TLS in TLS is said of Trojan and of VLESS without Vision, and of nothing else: Vision
// hides the records of the client's own TLS, and the other protocols have no TLS of their
// own around them, or pad it (AnyTLS).
func TestNestedTLS(t *testing.T) {
	for _, p := range All {
		want := (p.Type == "trojan" || p.Type == "vless") && !strings.Contains(p.ID, "vision")
		if p.NestedTLS != want {
			t.Errorf("%s: nested TLS %v, want %v", p.ID, p.NestedTLS, want)
		}
	}
}
