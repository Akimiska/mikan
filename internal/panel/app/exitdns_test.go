package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The DNS at the exit from the admin's side: on with the host's resolver by default, a
// wrong server named, an own list put in its canonical form and kept under a preset.
func TestExitDNSAPI(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	path := "/" + adminPath + "/api/v1/exit-dns"
	var v struct {
		Sniff    bool                `json:"sniff"`
		Resolver string              `json:"resolver"`
		Servers  []string            `json:"servers"`
		Presets  map[string][]string `json:"presets"`
	}
	resp, body := h.do(http.MethodGet, path, nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || !v.Sniff || v.Resolver != "host" || v.Servers == nil || len(v.Presets["cloudflare"]) != 2 {
		t.Fatalf("on by default: %d %s", resp.StatusCode, body)
	}

	resp, body = h.do(http.MethodPut, path, map[string]any{"sniff": true, "resolver": "custom", "servers": []string{"1.1.1.1", "https://1.1.1.1#PROXY"}}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "exit_dns_server") || !strings.Contains(string(body), "body.servers[1]") {
		t.Fatalf("a wrong server: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPut, path, map[string]any{"sniff": true, "resolver": "custom", "servers": []string{}}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "exit_dns_servers") {
		t.Fatalf("an empty own list: %d %s", resp.StatusCode, body)
	}

	resp, body = h.do(http.MethodPut, path, map[string]any{"sniff": false, "resolver": "custom", "servers": []string{"TLS://Dns.Google", "https://1.1.1.1", "tls://dns.google"}}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.Sniff || !slices.Equal(v.Servers, []string{"tls://dns.google", "https://1.1.1.1/dns-query"}) {
		t.Fatalf("an own list saved: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPut, path, map[string]any{"sniff": true, "resolver": "quad9", "servers": v.Servers}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.Resolver != "quad9" || len(v.Servers) != 2 {
		t.Fatalf("a preset keeps the own list: %d %s", resp.StatusCode, body)
	}
	resp, _ = h.do(http.MethodGet, path, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("read back")
	}
}
