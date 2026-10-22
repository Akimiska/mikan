package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"mikan/internal/panel/settings"
)

// The panel installs as an app from its secret path alone: the manifest is there, named
// after the admin's choice or the brand, and anywhere else is the bare 404 of a stranger.
func TestManifestUnderTheAdminPathOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	set := settings.New(h.st.Q)
	name := func() string {
		t.Helper()
		resp, body := h.do(http.MethodGet, "/"+adminPath+"/manifest.webmanifest", nil, nil)
		var m struct {
			Name     string `json:"name"`
			StartURL string `json:"start_url"`
			Scope    string `json:"scope"`
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/manifest+json" || json.Unmarshal(body, &m) != nil {
			t.Fatalf("manifest: %d %s", resp.StatusCode, body)
		}
		if m.StartURL != "./" || m.Scope != "./" {
			t.Fatalf("the app must open under the admin path: %+v", m)
		}
		return m.Name
	}
	if got := name(); got != "mikan" {
		t.Fatalf("no brand: %q", got)
	}
	if err := settings.Set(ctx, set, settings.KeyBrand, "AuraVPN"); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "AuraVPN" {
		t.Fatalf("the brand: %q", got)
	}
	if err := settings.Set(ctx, set, settings.KeyPanelAppName, "Аура · панель"); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "Аура · панель" {
		t.Fatalf("the admin's own: %q", got)
	}

	for _, p := range []string{"/manifest.webmanifest", "/" + subPath + "/manifest.webmanifest"} {
		if resp, body := h.do(http.MethodGet, p, nil, nil); resp.StatusCode != http.StatusNotFound || string(body) != "404 Not Found\n" {
			t.Errorf("%s: %d %q, want the bare 404", p, resp.StatusCode, body)
		}
	}
}

// PATCH /settings keeps the installed panel's name trimmed, and an empty one goes back to
// the brand.
func TestPanelAppNameSetting(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1/settings"
	var v struct {
		PanelAppName string `json:"panel_app_name"`
	}
	resp, body := h.do(http.MethodPatch, api, map[string]any{"panel_app_name": "  Моя панель  "}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.PanelAppName != "Моя панель" {
		t.Fatalf("set: %d %s", resp.StatusCode, body)
	}
	if resp, _ := h.do(http.MethodPatch, api, map[string]any{"panel_app_name": "это название намного длиннее тридцати символов"}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a name past 30 letters: %d", resp.StatusCode)
	}
	resp, body = h.do(http.MethodPatch, api, map[string]any{"panel_app_name": ""}, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil || v.PanelAppName != "" {
		t.Fatalf("back to the brand: %d %s", resp.StatusCode, body)
	}
}
