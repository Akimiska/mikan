package proto

import (
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

// The protocols of mihomo's newer cores: TrustTunnel, ShadowQUIC and Mieru check every
// user; Shadowsocks-2022, Sudoku and Snell have one key for everyone.
func TestExtraTypes(t *testing.T) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	ssKey := base64.StdEncoding.EncodeToString(key)
	cert := Cert{CertPath: "/c", KeyPath: "/k"}
	in := ClientInput{Name: "X", Host: "vpn.example.com", Port: 4443, SNI: "vpn.example.com", Slot: slots[0]}
	cases := []struct {
		src    string
		shared bool
		check  func(c Client, l map[string]any)
	}{
		{"type: trusttunnel\ncongestion-controller: bbr\n", false, func(c Client, l map[string]any) {
			us, _ := l["users"].([]map[string]any)
			if len(us) != 2 || us[0]["username"] != "s000001" || us[0]["password"] != "S3cr3t+/=" || l["certificate"] != "/c" {
				t.Errorf("trusttunnel listener: %v", l)
			}
			if n, _ := l["network"].([]any); len(n) != 1 || n[0] != "tcp" {
				t.Errorf("trusttunnel runs HTTP/2 over TCP only: %v", l["network"])
			}
			m := c.Mihomo
			if m["type"] != "trusttunnel" || m["username"] != "s000001" || m["password"] != "S3cr3t+/=" || m["sni"] != "vpn.example.com" || m["client-fingerprint"] != "chrome" || m["udp"] != true || m["tls"] != nil {
				t.Errorf("trusttunnel client: %v", m)
			}
			if c.URI != "" {
				t.Errorf("no share link format: %s", c.URI)
			}
		}},
		{"type: shadowquic\njls-upstream: {addr: www.google.com:443, sni: www.google.com}\nalpn: [h3]\n", false, func(c Client, l map[string]any) {
			us, _ := l["users"].([]map[string]any)
			if len(us) != 2 || us[1]["username"] != "s000002" || us[1]["password"] != "x" || l["certificate"] != nil {
				t.Errorf("shadowquic listener: %v", l)
			}
			m := c.Mihomo
			if m["type"] != "shadowquic" || m["username"] != "s000001" || m["password"] != "S3cr3t+/=" || m["sni"] != "www.google.com" || m["udp"] != true {
				t.Errorf("shadowquic client: %v", m)
			}
			if a, _ := m["alpn"].([]string); len(a) != 1 || a[0] != "h3" {
				t.Errorf("shadowquic alpn: %v", m["alpn"])
			}
		}},
		{"type: mieru\n", false, func(c Client, l map[string]any) {
			us, _ := l["users"].(map[string]string)
			if len(us) != 2 || us["s000001"] != "S3cr3t+/=" || l["transport"] != "TCP" {
				t.Errorf("mieru listener: %v", l)
			}
			m := c.Mihomo
			// mihomo 1.19.31 does not tell the node who sends UDP over Mieru: TCP only.
			if m["type"] != "mieru" || m["transport"] != "TCP" || m["username"] != "s000001" || m["password"] != "S3cr3t+/=" || m["udp"] != false {
				t.Errorf("mieru client: %v", m)
			}
		}},
		{"type: shadowsocks\ncipher: 2022-blake3-aes-128-gcm\npassword: " + ssKey + "\n", true, func(c Client, l map[string]any) {
			if _, has := l["users"]; has {
				t.Errorf("one key, no users: %v", l)
			}
			m := c.Mihomo
			if m["type"] != "ss" || m["cipher"] != "2022-blake3-aes-128-gcm" || m["password"] != ssKey || m["udp"] != false {
				t.Errorf("shadowsocks client: %v", m)
			}
			u, err := url.Parse(c.URI)
			if err != nil || u.Scheme != "ss" || u.User.Username() != "2022-blake3-aes-128-gcm" || u.Host != "vpn.example.com:4443" {
				t.Fatalf("ss link: %s %v", c.URI, err)
			}
			if p, _ := u.User.Password(); p != ssKey {
				t.Errorf("ss link key: %q", p)
			}
		}},
		{"type: sudoku\nkey: 6f1a44b8-c4e1-4a36-9d62-0b4ddc4c7c4f\naead-method: chacha20-poly1305\npadding-min: 2\npadding-max: 7\ntable-type: prefer_ascii\n", true, func(c Client, l map[string]any) {
			if _, has := l["users"]; has {
				t.Errorf("one key, no users: %v", l)
			}
			m := c.Mihomo
			if m["type"] != "sudoku" || m["key"] != "6f1a44b8-c4e1-4a36-9d62-0b4ddc4c7c4f" || m["aead-method"] != "chacha20-poly1305" || m["table-type"] != "prefer_ascii" || m["padding-max"] != 7 {
				t.Errorf("sudoku client: %v", m)
			}
		}},
		{"type: snell\npsk: p5k-Secret\nversion: 3\n", true, func(c Client, l map[string]any) {
			m := c.Mihomo
			if m["type"] != "snell" || m["psk"] != "p5k-Secret" || m["version"] != 3 || c.URI != "" {
				t.Errorf("snell client: %v %s", m, c.URI)
			}
		}},
	}
	for _, tc := range cases {
		tpl := mustParse(t, tc.src)
		if err := Validate(tpl, Options{}); err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		if Shared(tpl.Type()) != tc.shared {
			t.Errorf("%s: shared %v", tpl.Type(), Shared(tpl.Type()))
		}
		l, err := Listener(tpl, "n", "", "4443", slots, cert, Options{})
		if err != nil {
			t.Fatalf("%s listener: %v", tc.src, err)
		}
		c, err := ClientConfig(tpl, in)
		if err != nil {
			t.Fatalf("%s client: %v", tc.src, err)
		}
		tc.check(c, l)
	}
}

func TestExtraTypesRefuseWeakSettings(t *testing.T) {
	for src, want := range map[string]string{
		"type: shadowquic\n": "jls_upstream",
		"type: shadowquic\njls-upstream: {addr: 10.0.0.5:443}\n":                            "jls_upstream_private",
		"type: shadowquic\njls-upstream: {addr: www.google.com:443, proxy: out}\n":          "config_key",
		"type: shadowquic\njls-upstream: {addr: www.google.com:443}\nzero-rtt: true\n":      "config_key",
		"type: mieru\ntransport: UDP\n":                                                     "mieru_transport",
		"type: shadowsocks\ncipher: aes-128-gcm\npassword: x\n":                             "ss_cipher",
		"type: shadowsocks\ncipher: 2022-blake3-aes-256-gcm\npassword: c2hvcnQ=\n":          "ss_password",
		"type: shadowsocks\ncipher: 2022-blake3-aes-128-gcm\npassword: x\nshadow-tls: {}\n": "config_key",
		"type: sudoku\nkey: k\naead-method: none\n":                                         "sudoku_aead",
		"type: sudoku\naead-method: chacha20-poly1305\n":                                    "sudoku_key",
		"type: sudoku\nkey: k\nfallback: 127.0.0.1:80\n":                                    "config_key",
		"type: sudoku\nkey: k\nhttpmask: {mode: ws, path-root: x, fallback: y}\n":           "config_key",
		"type: snell\npsk: \"\"\n":                                                          "snell_psk",
		"type: snell\npsk: x\nversion: 9\n":                                                 "snell_version",
		"type: trusttunnel\nnetwork: [tcp, udp]\n":                                          "config_key",
	} {
		if err := Validate(mustParse(t, src), Options{}); code(err) != want {
			t.Errorf("%q: got %v, want %s", src, err, want)
		}
	}
}

func TestNeeds(t *testing.T) {
	priv, _ := realityKey(t)
	reality := "reality-config: {dest: www.microsoft.com:443, private-key: " + priv + ", short-id: [ab], server-names: [www.microsoft.com]}\n"
	for src, want := range map[string]Needs{
		"type: vless\nxhttp-config: {path: /x, mode: stream-one}\n" + reality: {Type: "vless", Transport: "xhttp"},
		"type: vless\nmikan: {flow: xtls-rprx-vision}\n" + reality:            {Type: "vless", Transport: "tcp"},
		"type: trojan\ngrpc-service-name: g\n" + reality:                      {Type: "trojan", Transport: "grpc"},
		"type: tuic\n":        {Type: "tuic"},
		"type: trusttunnel\n": {Type: "trusttunnel"},
	} {
		if got := NeedsOf(mustParse(t, src)); got != want {
			t.Errorf("%s: %+v, want %+v", strings.SplitN(src, "\n", 2)[0], got, want)
		}
	}
	pq := mustParse(t, "type: vless\nxhttp-config: {path: /x, mode: stream-one}\n"+reality+"decryption: mlkem768x25519plus.native.600s."+testX25519(t)+"\n")
	if n := NeedsOf(pq); !n.Encryption || n.Transport != "xhttp" {
		t.Errorf("pq: %+v", n)
	}
}

func testX25519(t *testing.T) string {
	t.Helper()
	priv, _ := realityKey(t)
	return priv
}
