package app

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// The traffic filters from the admin's side: off by default with the mail preset ready,
// a wrong entry named, the lists put in order, each filter replaced on its own.
func TestFiltersAPI(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	path := "/" + adminPath + "/api/v1/filters"
	type egress struct {
		Enabled  bool     `json:"enabled"`
		Mail     bool     `json:"mail"`
		Ports    []string `json:"ports"`
		Networks []string `json:"networks"`
		Domains  []string `json:"domains"`
	}
	type ingress struct {
		Enabled  bool     `json:"enabled"`
		Allow    bool     `json:"allow"`
		Networks []string `json:"networks"`
	}
	var v struct {
		Egress    egress   `json:"egress"`
		Ingress   ingress  `json:"ingress"`
		MailPorts []string `json:"mail_ports"`
	}
	resp, body := h.do(http.MethodGet, path, nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.Egress.Enabled || !v.Egress.Mail || v.Ingress.Enabled ||
		v.Egress.Ports == nil || v.Ingress.Networks == nil || !slices.Equal(v.MailPorts, []string{"465", "587", "2525"}) {
		t.Fatalf("off by default, the mail preset ready: %d %s", resp.StatusCode, body)
	}

	bad := egress{Enabled: true, Mail: true, Ports: []string{"465"}, Networks: []string{}, Domains: []string{"ok.example", "no way"}}
	resp, body = h.do(http.MethodPatch, path, map[string]any{"egress": bad}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "filters_entry") || !strings.Contains(string(body), "body.egress.domains[1]") {
		t.Fatalf("a wrong domain: %d %s", resp.StatusCode, body)
	}

	good := egress{Enabled: true, Mail: true, Ports: []string{"6881-6889", "1194"}, Networks: []string{"203.0.113.7/24"}, Domains: []string{"*.Spam.Example"}}
	resp, body = h.do(http.MethodPatch, path, map[string]any{"egress": good}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || !v.Egress.Enabled ||
		!slices.Equal(v.Egress.Ports, []string{"1194", "6881-6889"}) || v.Egress.Networks[0] != "203.0.113.0/24" || v.Egress.Domains[0] != "spam.example" {
		t.Fatalf("egress saved in order: %d %s", resp.StatusCode, body)
	}

	resp, body = h.do(http.MethodPatch, path, map[string]any{"ingress": ingress{Enabled: true, Allow: true, Networks: []string{"198.51.100.7"}}}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || !v.Ingress.Allow || v.Ingress.Networks[0] != "198.51.100.7/32" || !v.Egress.Enabled || len(v.Egress.Ports) != 2 {
		t.Fatalf("ingress saved, egress kept: %d %s", resp.StatusCode, body)
	}
}
