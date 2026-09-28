package subs

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"mikan/internal/nodeapi"
	"mikan/internal/panel/presets"
	"mikan/internal/panel/store/db"
)

func TestFormat(t *testing.T) {
	cases := map[string]string{
		"clash-verge/v2.2.3":         "clash",
		"FlClash/v0.8.80 clash-meta": "clash",
		"mihomo/1.19.31":             "clash",
		"Stash/3.1.1 Clash/1.9.0":    "clash",
		"Happ/3.4.1":                 "uri",
		"v2rayNG/1.10.2":             "uri",
		"v2RayTun/Android":           "uri",
		"Streisand/1.6":              "uri",
		"HiddifyNext/2.5.7":          "uri",
		"curl/8.9":                   "uri",
	}
	for ua, want := range cases {
		if got := Format(ua, "*/*", ""); got != want {
			t.Errorf("%q → %s, want %s", ua, got, want)
		}
	}
	if Format("Mozilla/5.0 (iPhone)", "text/html,application/xhtml+xml", "") != "html" {
		t.Error("browser must get the page")
	}
	if Format("Happ/3", "", "clash") != "clash" {
		t.Error("?format= must override the User-Agent")
	}
}

func profile(t *testing.T, pin string) Profile {
	t.Helper()
	var ins []db.Inbound
	for i, p := range presets.All {
		s, err := presets.NewSettings(p.ID, "www.example.com:443")
		if err != nil {
			t.Fatal(err)
		}
		ins = append(ins, db.Inbound{ID: int64(i + 1), Name: p.Name, Preset: p.ID, Port: p.Port, Enabled: 1, Settings: string(s)})
	}
	return Profile{
		Slot:     db.Slot{Name: "s000001", Uuid: "0b4ddc4c-7c4f-4a36-9d62-6f1a44b8c4e1", Secret: "S3cr3t+/="},
		Inbounds: ins,
		Endpoint: Endpoint{Host: "203.0.113.7", PinSHA256: pin},
	}
}

func TestURIs(t *testing.T) {
	links, err := URIs(profile(t, "ab12"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(links, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d links", len(lines))
	}
	vision, err := url.Parse(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	q := vision.Query()
	if vision.Scheme != "vless" || vision.User.Username() != "0b4ddc4c-7c4f-4a36-9d62-6f1a44b8c4e1" || vision.Host != "203.0.113.7:443" ||
		q.Get("flow") != "xtls-rprx-vision" || q.Get("security") != "reality" || q.Get("pbk") == "" || q.Get("sid") == "" || q.Get("sni") != "www.example.com" {
		t.Fatalf("vision link: %s", lines[0])
	}
	xhttp, _ := url.Parse(lines[1])
	if xhttp.Query().Get("type") != "xhttp" || xhttp.Query().Get("mode") != "stream-one" || !strings.HasPrefix(xhttp.Query().Get("path"), "/") {
		t.Fatalf("xhttp link: %s", lines[1])
	}
	hy2, _ := url.Parse(lines[2])
	if hy2.Scheme != "hysteria2" || hy2.User.Username() != "S3cr3t+/=" || hy2.Query().Get("pinSHA256") != "ab12" || hy2.Query().Get("obfs") != "salamander" {
		t.Fatalf("hy2 link: %s", lines[2])
	}
	tuic, _ := url.Parse(lines[3])
	pw, _ := tuic.User.Password()
	if tuic.Scheme != "tuic" || pw != "S3cr3t+/=" || tuic.Query().Get("allow_insecure") != "1" {
		t.Fatalf("tuic link: %s", lines[3])
	}
}

func TestURIsWithRealCertificateDoNotPin(t *testing.T) {
	links, err := URIs(profile(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(links, "insecure") || strings.Contains(links, "pinSHA256") {
		t.Fatalf("real certificate must be verified normally: %s", links)
	}
}

func TestMihomoProfile(t *testing.T) {
	raw, err := Mihomo(profile(t, "ab12"), []string{"MATCH,VPN"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []map[string]any `json:"proxies"`
		Groups  []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Proxies) != 4 || cfg.Groups[0].Name != "VPN" || cfg.Rules[0] != "MATCH,VPN" {
		t.Fatalf("profile: %s", raw)
	}
	byType := map[string]map[string]any{}
	for _, p := range cfg.Proxies {
		byType[p["name"].(string)] = p
	}
	if byType["VLESS XHTTP"]["network"] != "xhttp" || byType["VLESS Vision"]["flow"] != "xtls-rprx-vision" {
		t.Fatalf("vless proxies: %v", cfg.Proxies)
	}
	if byType["Hysteria2"]["fingerprint"] != "ab12" || byType["TUIC"]["password"] != "S3cr3t+/=" {
		t.Fatalf("quic proxies: %v", cfg.Proxies)
	}
	_ = nodeapi.PresetTUIC
}
