package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The admin picks the default language in the settings; "auto" hands it back to the
// browser. The auto group's default name and the bot's untouched menu follow it.
func TestDefaultLangSetting(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1/settings"
	type view struct {
		DefaultLang  string `json:"default_lang"`
		SubGroupAuto string `json:"sub_group_auto"`
	}
	read := func(resp *http.Response, body []byte) view {
		t.Helper()
		var v view
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil {
			t.Fatalf("settings: %d %s", resp.StatusCode, body)
		}
		return v
	}
	if v := read(h.do(http.MethodGet, api, nil, nil)); v != (view{"auto", "Авто"}) {
		t.Fatalf("a panel set up without a language: %+v", v)
	}
	if v := read(h.do(http.MethodPatch, api, map[string]any{"default_lang": "en"}, csrf)); v != (view{"en", "Auto"}) {
		t.Fatalf("en: %+v", v)
	}
	if resp, body := h.do(http.MethodGet, "/"+adminPath+"/api/v1/telegram", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"lang":"en"`) {
		t.Fatalf("the bot's untouched menu follows the language: %d %s", resp.StatusCode, body)
	}
	if resp, body := h.do(http.MethodPatch, api, map[string]any{"default_lang": "de"}, csrf); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown language: %d %s", resp.StatusCode, body)
	}
	if v := read(h.do(http.MethodPatch, api, map[string]any{"default_lang": "auto"}, csrf)); v != (view{"auto", "Авто"}) {
		t.Fatalf("auto: %+v", v)
	}
}
