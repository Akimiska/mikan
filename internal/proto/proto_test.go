package proto

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func realityKey(t *testing.T) (private, public string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b := base64.RawURLEncoding
	return b.EncodeToString(k.Bytes()), b.EncodeToString(k.PublicKey().Bytes())
}

func mustParse(t *testing.T, src string) Template {
	t.Helper()
	tpl, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

var slots = []Slot{{Name: "s000001", UUID: "0b4ddc4c-7c4f-4a36-9d62-6f1a44b8c4e1", Secret: "S3cr3t+/="}, {Name: "s000002", UUID: "1c5eed5d-8d5a-4b47-8e73-7a2b55c9d5f2", Secret: "x"}}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestPresetsConvertAndRender(t *testing.T) {
	priv, pub := realityKey(t)
	reality := `{"reality":{"private_key":"` + priv + `","short_ids":["a1b2c3d4"],"dest":"www.microsoft.com:443","server_names":["www.microsoft.com"]}`
	cases := map[string]string{
		"vless_reality_vision": reality + `}`,
		"vless_reality_xhttp":  reality + `,"path":"/x7k2","mode":"stream-one"}`,
		"hysteria2":            `{"obfs_password":"obfs123"}`,
		"tuic_v5":              `{"congestion_control":"bbr"}`,
	}
	cert := Cert{CertPath: "/data/tls/cert.pem", KeyPath: "/data/tls/key.pem"}
	for preset, settings := range cases {
		tpl, err := FromPreset(preset, []byte(settings))
		if err != nil {
			t.Fatalf("%s: %v", preset, err)
		}
		// Round trip through YAML: what the panel stores and the editor shows.
		tpl = mustParse(t, Marshal(tpl))
		if err := Validate(tpl, Options{}); err != nil {
			t.Fatalf("%s: %v\n%s", preset, err, Marshal(tpl))
		}
		l, err := Listener(tpl, "in-"+preset, "", "443", slots, cert, Options{})
		if err != nil {
			t.Fatalf("%s listener: %v", preset, err)
		}
		if l["name"] != "in-"+preset || l["port"] != "443" || l["listen"] != "0.0.0.0" || l[extKey] != nil {
			t.Fatalf("%s listener: %v", preset, l)
		}
		c, err := ClientConfig(tpl, ClientInput{Name: "N", Host: "203.0.113.7", Port: 443, PortSpec: "443", PinSHA256: "ab12", Slot: slots[0]})
		if err != nil {
			t.Fatalf("%s client: %v", preset, err)
		}
		u, err := url.Parse(c.URI)
		if err != nil {
			t.Fatalf("%s uri: %v", preset, err)
		}
		q := u.Query()
		switch preset {
		case "vless_reality_vision":
			us := l["users"].([]map[string]any)
			if len(us) != 2 || us[0]["flow"] != "xtls-rprx-vision" || us[0]["username"] != "s000001" || l["certificate"] != nil {
				t.Fatalf("vision users: %v", l)
			}
			if q.Get("pbk") != pub || q.Get("flow") != "xtls-rprx-vision" || q.Get("type") != "tcp" || q.Get("sni") != "www.microsoft.com" || c.Mihomo["udp"] != true {
				t.Fatalf("vision client: %s %v", c.URI, c.Mihomo)
			}
		case "vless_reality_xhttp":
			if q.Get("type") != "xhttp" || q.Get("path") != "/x7k2" || q.Get("mode") != "stream-one" || q.Get("flow") != "" || c.Mihomo["udp"] != false {
				t.Fatalf("xhttp client: %s %v", c.URI, c.Mihomo)
			}
			if opts := c.Mihomo["xhttp-opts"].(map[string]any); opts["reuse-settings"] == nil {
				t.Fatalf("xhttp must multiplex: %v", opts)
			}
		case "hysteria2":
			if users := l["users"].(map[string]string); users["s000001"] != "S3cr3t+/=" || l["certificate"] != cert.CertPath {
				t.Fatalf("hy2 listener: %v", l)
			}
			if u.Scheme != "hysteria2" || q.Get("obfs") != "salamander" || q.Get("pinSHA256") != "ab12" || c.Mihomo["fingerprint"] != "ab12" {
				t.Fatalf("hy2 client: %s %v", c.URI, c.Mihomo)
			}
		case "tuic_v5":
			if users := l["users"].(map[string]string); users[slots[0].UUID] != "S3cr3t+/=" {
				t.Fatalf("tuic users keyed by uuid: %v", l)
			}
			if pw, _ := u.User.Password(); u.Scheme != "tuic" || pw != "S3cr3t+/=" || q.Get("allow_insecure") != "1" || c.Mihomo["congestion-controller"] != "bbr" {
				t.Fatalf("tuic client: %s %v", c.URI, c.Mihomo)
			}
		}
	}
}

// REALITY listeners bound the client's clock skew, so a recorded ClientHello cannot be
// replayed later to see the REALITY certificate; a template's own value wins.
func TestRealityReplayWindow(t *testing.T) {
	priv, _ := realityKey(t)
	src := "type: vless\nreality-config:\n  dest: www.microsoft.com:443\n  private-key: " + priv + "\n  short-id: [a1b2]\n  server-names: [www.microsoft.com]\n"
	// Numbers from YAML come as float64, the default as int64; mihomo takes both.
	window := func(src string) string {
		l, err := Listener(mustParse(t, src), "n", "", "443", slots, Cert{}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%.0f", toFloat(l["reality-config"].(map[string]any)["max-time-difference"]))
	}
	if got := window(src); got != "7200000000" {
		t.Fatalf("default window: %s (mihomo reads microseconds: 2 h = 7200000000)", got)
	}
	if got := window(src + "  max-time-difference: 60000000\n"); got != "60000000" {
		t.Fatalf("the template's own window: %s", got)
	}
	// The template itself stays as the admin wrote it.
	tpl := mustParse(t, src)
	if _, err := Listener(tpl, "n", "", "443", slots, Cert{}, Options{}); err != nil || tpl.section("reality-config")["max-time-difference"] != nil {
		t.Fatalf("the stored template changed: %v", tpl)
	}
}

// VLESS Encryption: the listener keeps the private keys, clients get the public halves
// with a full handshake (1rtt), in both subscription formats.
func TestVLESSEncryption(t *testing.T) {
	priv, _ := realityKey(t)
	x := make([]byte, 32)
	_, _ = rand.Read(x)
	xk, _ := ecdh.X25519().NewPrivateKey(x)
	seed := make([]byte, mlkem.SeedSize)
	_, _ = rand.Read(seed)
	dk, _ := mlkem.NewDecapsulationKey768(seed)
	b64 := base64.RawURLEncoding.EncodeToString

	got, err := ClientEncryption("mlkem768x25519plus.xorpub.300-600s.100-500.50-100." + b64(x) + "." + b64(seed))
	want := "mlkem768x25519plus.xorpub.1rtt.100-500.50-100." + b64(xk.PublicKey().Bytes()) + "." + b64(dk.EncapsulationKey().Bytes())
	if err != nil || got != want {
		t.Fatalf("client encryption:\n got %s (%v)\nwant %s", got, err, want)
	}
	for _, bad := range []string{
		"mlkem768x25519plus.native.600s",                // no key
		"aes.native.600s." + b64(x),                     // other method
		"mlkem768x25519plus.plain.600s." + b64(x),       // unknown mode
		"mlkem768x25519plus.native.10m." + b64(x),       // ticket is seconds
		"mlkem768x25519plus.native.600s." + b64(x[:31]), // short key
	} {
		if _, err := ClientEncryption(bad); code(err) != "vless_decryption" {
			t.Errorf("%s: %v", bad, err)
		}
	}

	src := "type: vless\nxhttp-config: {path: /p, mode: stream-one}\ndecryption: mlkem768x25519plus.native.600s." + b64(x) +
		"\nreality-config:\n  dest: www.microsoft.com:443\n  private-key: " + priv + "\n  short-id: [a1b2]\n  server-names: [www.microsoft.com]\n"
	tpl := mustParse(t, src)
	l, err := Listener(tpl, "n", "", "2096", slots, Cert{}, Options{})
	if err != nil || l["decryption"] != "mlkem768x25519plus.native.600s."+b64(x) {
		t.Fatalf("listener keeps the decryption: %v %v", err, l["decryption"])
	}
	c, err := ClientConfig(tpl, ClientInput{Name: "PQ", Host: "203.0.113.7", Port: 2096, Slot: slots[0]})
	if err != nil {
		t.Fatal(err)
	}
	enc := "mlkem768x25519plus.native.1rtt." + b64(xk.PublicKey().Bytes())
	u, _ := url.Parse(c.URI)
	if c.Mihomo["encryption"] != enc || u.Query().Get("encryption") != enc || strings.Contains(c.URI, b64(x)) {
		t.Fatalf("client: %v\n%s", c.Mihomo["encryption"], c.URI)
	}
	if code(Validate(mustParse(t, "type: vless\ndecryption: nope\n"+src[strings.Index(src, "reality-config"):]), Options{})) != "vless_decryption" {
		t.Fatal("a broken decryption must be refused")
	}
	if code(Validate(mustParse(t, "type: trojan\ndecryption: none\nmikan: {tls: node}\n"), Options{})) != "config_key" {
		t.Fatal("decryption is VLESS only")
	}
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	}
	return -1
}

// A self-signed node certificate is pinned in links too: Xray apps read pcs, Hysteria2
// apps pinSHA256. TUIC links have no pin field.
func TestSelfSignedLinksCarryThePin(t *testing.T) {
	in := ClientInput{Name: "X", Host: "203.0.113.7", Port: 2083, PinSHA256: "ab12", Slot: slots[0]}
	for src, want := range map[string]string{
		"type: trojan\nmikan: {tls: node}\n":             "pcs=ab12",
		"type: anytls\n":                                 "pcs=ab12",
		"type: vless\nws-path: /v\nmikan: {tls: node}\n": "pcs=ab12",
		"type: hysteria2\n":                              "pinSHA256=ab12",
		"type: vmess\nws-path: /m\nmikan: {tls: node}\n": `"pcs":"ab12"`,
	} {
		c, err := ClientConfig(mustParse(t, src), in)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		link := c.URI
		if strings.HasPrefix(link, "vmess://") {
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
			link = string(raw)
		}
		if !strings.Contains(link, want) || c.Mihomo["fingerprint"] != "ab12" {
			t.Errorf("%s: %s lacks %s (mihomo %v)", src, link, want, c.Mihomo["fingerprint"])
		}
	}
}

func TestValidateRefusesDangerousTemplates(t *testing.T) {
	priv, _ := realityKey(t)
	reality := "reality-config:\n  dest: www.microsoft.com:443\n  private-key: " + priv + "\n  short-id: [a1b2]\n  server-names: [www.microsoft.com]\n"
	cases := map[string]struct{ src, code string }{
		"managed users":         {"type: vless\n" + reality + "users: []\n", "config_managed"},
		"cert path":             {"type: hysteria2\ncertificate: /etc/shadow\n", "config_managed"},
		"routing bypass":        {"type: vless\n" + reality + "proxy: DIRECT\n", "config_key"},
		"plain vless":           {"type: vless\nws-path: /ws\n", "config_insecure"},
		"reality and tls":       {"type: vless\n" + reality + "mikan: {tls: node}\n", "config_both_tls"},
		"private dest":          {strings.Replace("type: vless\n"+reality, "www.microsoft.com:443", "169.254.169.254:80", 1), "reality_dest_private"},
		"reality proxy":         {"type: vless\n" + reality + "  proxy: DIRECT\n", "config_key"},
		"file masquerade":       {"type: hysteria2\nmasquerade: file:///etc\n", "config_masquerade"},
		"private masquerade":    {"type: hysteria2\nmasquerade: https://10.0.0.1/\n", "config_masquerade"},
		"realm":                 {"type: hysteria2\nrealm-opts: {enable: true}\n", "config_key"},
		"vision over xhttp":     {"type: vless\n" + reality + "xhttp-config: {path: /x}\nmikan: {flow: xtls-rprx-vision}\n", "config_flow"},
		"xhttp auto":            {"type: vless\n" + reality + "xhttp-config: {path: /x, mode: auto}\n", "config_xhttp_mode"},
		"shadowsocks pre-2022":  {"type: shadowsocks\npassword: x\n", "ss_cipher"},
		"unencrypted socks":     {"type: socks\n", "config_type"},
		"unknown mikan key":     {"type: tuic\nmikan: {evil: 1}\n", "config_key"},
		"bad short id":          {strings.Replace("type: vless\n"+reality, "[a1b2]", "[xyz]", 1), "reality_sid"},
		"obfs without password": {"type: hysteria2\nobfs: salamander\n", "config_obfs"},
	}
	for name, c := range cases {
		tpl, err := Parse(c.src)
		if err == nil {
			err = Validate(tpl, Options{})
		}
		if code(err) != c.code {
			t.Errorf("%s: got %v, want %s", name, err, c.code)
		}
	}
}

func TestSelfStealDest(t *testing.T) {
	priv, _ := realityKey(t)
	src := "type: vless\nreality-config:\n  dest: 127.0.0.1:21355\n  private-key: " + priv + "\n  short-id: [a1b2]\n  server-names: [vpn.example.com]\n"
	if code(Validate(mustParse(t, src), Options{})) != "reality_dest_private" {
		t.Fatal("loopback dest must be refused without self-steal")
	}
	if code(Validate(mustParse(t, src), Options{SelfStealPort: 22})) != "reality_dest_private" {
		t.Fatal("only the panel's port is a self-steal target")
	}
	if err := Validate(mustParse(t, src), Options{SelfStealPort: 21355}); err != nil {
		t.Fatalf("the panel's own port is the self-steal target: %v", err)
	}
}

func TestNewTypes(t *testing.T) {
	priv, _ := realityKey(t)
	cert := Cert{CertPath: "/c", KeyPath: "/k"}
	in := ClientInput{Name: "X", Host: "vpn.example.com", Port: 2053, SNI: "vpn.example.com", Slot: slots[0]}
	cases := map[string]func(Client, map[string]any){
		"type: vless\ngrpc-service-name: api\nreality-config: {dest: www.microsoft.com:443, private-key: " + priv + ", short-id: [ab], server-names: [www.microsoft.com]}\n": func(c Client, l map[string]any) {
			if !strings.Contains(c.URI, "type=grpc") || !strings.Contains(c.URI, "serviceName=api") || c.Mihomo["network"] != "grpc" {
				t.Errorf("grpc: %s %v", c.URI, c.Mihomo)
			}
		},
		"type: trojan\nws-path: /t\nmikan: {tls: node}\n": func(c Client, l map[string]any) {
			if l["certificate"] != "/c" || !strings.HasPrefix(c.URI, "trojan://") || c.Mihomo["sni"] != "vpn.example.com" || c.Mihomo["password"] != "S3cr3t+/=" {
				t.Errorf("trojan: %s %v %v", c.URI, c.Mihomo, l)
			}
			if us := l["users"].([]map[string]any); us[0]["password"] != "S3cr3t+/=" {
				t.Errorf("trojan users: %v", us)
			}
		},
		"type: anytls\n": func(c Client, l map[string]any) {
			if l["certificate"] != "/c" || !strings.HasPrefix(c.URI, "anytls://") || c.Mihomo["sni"] != "vpn.example.com" {
				t.Errorf("anytls: %s %v", c.URI, c.Mihomo)
			}
		},
		"type: vmess\nws-path: /v\nmikan: {tls: node, client: {server: cdn.example.com, port: 443}}\n": func(c Client, l map[string]any) {
			if c.Mihomo["server"] != "cdn.example.com" || c.Mihomo["port"] != 443 || !strings.HasPrefix(c.URI, "vmess://") {
				t.Errorf("vmess override: %s %v", c.URI, c.Mihomo)
			}
		},
	}
	for src, check := range cases {
		tpl := mustParse(t, src)
		l, err := Listener(tpl, "n", "", "2053", slots, cert, Options{})
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		c, err := ClientConfig(tpl, in)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		check(c, l)
	}
}

func TestMarshalKeepsTypeFirst(t *testing.T) {
	out := Marshal(Template{"alpn": []any{"h3"}, "type": "tuic", "mikan": map[string]any{"tls": "node"}, "congestion-controller": "bbr"})
	if !strings.HasPrefix(out, "type: tuic\n") || !strings.HasSuffix(strings.TrimSpace(out), "tls: node") {
		t.Fatalf("order:\n%s", out)
	}
}

func TestParseErrorsAreCodes(t *testing.T) {
	if _, err := Parse("type: [unclosed"); code(err) != "config_yaml" {
		t.Fatalf("got %v", err)
	}
	if _, err := Parse("# only a comment\n"); code(err) != "config_empty" {
		t.Fatalf("got %v", err)
	}
}
