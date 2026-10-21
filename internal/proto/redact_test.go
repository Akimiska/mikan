package proto

import (
	"strings"
	"testing"
)

// A reader that may not see the secrets gets them as Hidden; the template that comes back
// with Hidden in place gets the saved values again, and one the reader changed keeps the
// change.
func TestRedactAndRestore(t *testing.T) {
	const saved = "type: vless\nreality-config:\n    dest: www.microsoft.com:443\n    private-key: SECRET-KEY\n    short-id: [ab]\n" +
		"decryption: mlkem768x25519plus.native.600s.SERVER-KEY\n"
	red := Redact(saved)
	for _, leak := range []string{"SECRET-KEY", "SERVER-KEY"} {
		if strings.Contains(red, leak) {
			t.Fatalf("redacted template holds %s:\n%s", leak, red)
		}
	}
	if !strings.Contains(red, Hidden) || !strings.Contains(red, "www.microsoft.com:443") || !strings.Contains(red, "ab") {
		t.Fatalf("redacted template lost what it may show:\n%s", red)
	}
	// Back unchanged but for the port of dest: the keys come back, the change stays.
	back := Restore(strings.Replace(red, "www.microsoft.com:443", "www.apple.com:443", 1), saved)
	got, err := Parse(back)
	if err != nil {
		t.Fatal(err)
	}
	r := got["reality-config"].(map[string]any)
	if r["private-key"] != "SECRET-KEY" || r["dest"] != "www.apple.com:443" || got.str("decryption") != "mlkem768x25519plus.native.600s.SERVER-KEY" {
		t.Fatalf("restored: %s", back)
	}
	// A secret the reader typed anew is theirs.
	back = Restore(strings.Replace(red, Hidden, "NEW-KEY", 1), saved)
	if !strings.Contains(back, "NEW-KEY") {
		t.Fatalf("a new key was replaced: %s", back)
	}
	// The shared protocols' one key and Hysteria2's obfuscation password are secrets too.
	for _, c := range []struct{ src, secret string }{
		{"type: shadowsocks\ncipher: aes-128-gcm\npassword: PASS\n", "PASS"},
		{"type: snell\npsk: PSK\nversion: 4\n", "PSK"},
		{"type: sudoku\nkey: SUDOKU\n", "SUDOKU"},
		{"type: hysteria2\nobfs: salamander\nobfs-password: OBFS\n", "OBFS"},
	} {
		red := Redact(c.src)
		if strings.Contains(red, c.secret) || !strings.Contains(red, Hidden) {
			t.Errorf("%q redacted: %s", c.src, red)
		}
		if back := Restore(red, c.src); !strings.Contains(back, c.secret) {
			t.Errorf("%q restored: %s", c.src, back)
		}
	}
	// Nothing to hide: the template as it is, its own formatting kept.
	const plain = "type: tuic\n# the admin's note\ncongestion-controller: bbr\n"
	if Redact(plain) != plain || Restore(plain, saved) != plain {
		t.Fatalf("a template without secrets changed: %q %q", Redact(plain), Restore(plain, saved))
	}
	// What does not parse is shown as nothing, never as it is.
	if Redact("type: [") != "" {
		t.Fatalf("an unparsed template was shown")
	}
}
