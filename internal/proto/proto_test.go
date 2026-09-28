package proto

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
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
		"shadowsocks":           {"type: shadowsocks\npassword: x\n", "config_type"},
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
