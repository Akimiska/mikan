package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppName(t *testing.T) {
	for _, c := range []struct{ stored, brand, want string }{
		{"", "", "mikan"},
		{"", "VPN", "mikan"}, // the brand of a panel that never set one
		{"", " AuraVPN ", "AuraVPN"},
		{"  Моя панель ", "AuraVPN", "Моя панель"},
		{"mikan", "AuraVPN", "mikan"},
		{"A\tB\x07", "", "AB"},
		{strings.Repeat("я", 40), "", strings.Repeat("я", AppNameMax)},
		{" \t ", "Brand", "Brand"},
	} {
		if got := AppName(c.stored, c.brand); got != c.want {
			t.Errorf("AppName(%q, %q) = %q, want %q", c.stored, c.brand, got, c.want)
		}
	}
}

func fetchManifest(t *testing.T, h http.Handler, method, acceptLang string) (*httptest.ResponseRecorder, manifest) {
	t.Helper()
	req := httptest.NewRequest(method, "/manifest.webmanifest", nil)
	if acceptLang != "" {
		req.Header.Set("Accept-Language", acceptLang)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var m manifest
	if method == http.MethodGet && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("manifest JSON: %v\n%s", err, rec.Body)
		}
	}
	return rec, m
}

// The manifest a browser installs the panel by: relative URLs only (the secret path stays
// out of it), the three icons Chrome asks for, the shortcuts in the panel's language.
func TestManifest(t *testing.T) {
	info := ManifestInfo{Name: "AuraVPN", Lang: "ru"}
	h := Manifest(func(context.Context) (ManifestInfo, error) { return info, nil })

	rec, m := fetchManifest(t, h, http.MethodGet, "en-US")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("answer: %d %v", rec.Code, rec.Header())
	}
	if m.Name != "AuraVPN" || m.ShortName != "AuraVPN" || m.StartURL != "./" || m.Scope != "./" || m.Display != "standalone" || m.Lang != "ru" {
		t.Fatalf("manifest: %+v", m)
	}
	var any192, any512, maskable bool
	for _, i := range m.Icons {
		any192 = any192 || i.Sizes == "192x192" && i.Purpose == "any"
		any512 = any512 || i.Sizes == "512x512" && i.Purpose == "any"
		maskable = maskable || i.Sizes == "512x512" && i.Purpose == "maskable"
		if strings.Contains(i.Src, "/") {
			t.Fatalf("icon %q is not relative to the manifest", i.Src)
		}
	}
	if !any192 || !any512 || !maskable {
		t.Fatalf("icons: %+v", m.Icons)
	}
	if len(m.Shortcuts) != 4 || m.Shortcuts[0].Name != "Пользователи" || m.Shortcuts[0].URL != "./users" {
		t.Fatalf("shortcuts: %+v", m.Shortcuts)
	}

	// No language of its own: the visitor's.
	info.Lang = ""
	if _, m := fetchManifest(t, h, http.MethodGet, "ru-RU,ru;q=0.9"); m.Lang != "ru" || m.Shortcuts[3].Name != "Настройки" {
		t.Fatalf("Russian visitor: %+v", m)
	}
	if _, m := fetchManifest(t, h, http.MethodGet, "de-DE"); m.Lang != "en" || m.Description != "mikan admin panel" {
		t.Fatalf("other visitor: %+v", m)
	}

	if rec, _ := fetchManifest(t, h, http.MethodHead, ""); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %q", rec.Code, rec.Body)
	}
	if rec, _ := fetchManifest(t, h, http.MethodPost, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
	failing := Manifest(func(context.Context) (ManifestInfo, error) { return ManifestInfo{}, context.Canceled })
	if rec, _ := fetchManifest(t, failing, http.MethodGet, ""); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "canceled") {
		t.Fatalf("a failed read: %d %q", rec.Code, rec.Body)
	}
}
