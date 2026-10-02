package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
)

// fakeMarzban answers like Marzban (or PasarGuard with ISO dates and ids) to an admin
// with the password "pw".
func fakeMarzban(t *testing.T, users []map[string]any) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/token":
			_ = r.ParseForm()
			if r.PostForm.Get("username") != "admin" || r.PostForm.Get("password") != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer"})
		case "/api/users":
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			page := users
			if r.URL.Query().Get("offset") != "0" {
				page = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"users": page, "total": len(users)})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeRemnawave(t *testing.T, users []map[string]any) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/stream" || r.Header.Get("Authorization") != "Bearer rw-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"users": users, "nextCursor": nil, "hasMore": false}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportFromMarzbanOverHTTP(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355} {
		if err := settings.Set(ctx, settings.New(h.st.Q), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	api := "/" + adminPath + "/api/v1"
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	tariffs, _ := h.st.Q.ListTariffs(ctx)

	old := fakeMarzban(t, []map[string]any{
		{"username": "ivan_petrov", "status": "active", "data_limit": 107374182400, "used_traffic": 5368709120, "lifetime_used_traffic": 21474836480, "expire": 1893456000, "note": "VIP"},
		{"username": "olga", "status": "disabled", "data_limit": nil, "used_traffic": 0, "lifetime_used_traffic": 0, "expire": nil},
		{"username": "pause", "status": "on_hold", "data_limit": 0, "used_traffic": 0, "lifetime_used_traffic": 0, "expire": nil, "on_hold_expire_duration": 2592000},
	})
	src := map[string]any{"kind": "marzban", "url": old.URL, "username": "admin", "password": "pw"}

	bad := map[string]any{"kind": "marzban", "url": old.URL, "username": "admin", "password": "wrong"}
	if resp, body := h.do(http.MethodPost, api+"/import/preview", bad, csrf); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "import_auth") {
		t.Fatalf("a wrong password: %d %s", resp.StatusCode, body)
	}
	var p struct {
		Total  int `json:"total"`
		New    int `json:"new"`
		OnHold int `json:"on_hold"`
	}
	resp, body := h.do(http.MethodPost, api+"/import/preview", src, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &p) != nil || p.Total != 3 || p.New != 3 || p.OnHold != 1 {
		t.Fatalf("preview: %d %s", resp.StatusCode, body)
	}

	run := map[string]any{"kind": "marzban", "url": old.URL, "username": "admin", "password": "pw", "tariff_id": tariffs[1].ID}
	var r struct {
		Created, Links int
		Skipped        []string
	}
	resp, body = h.do(http.MethodPost, api+"/import", run, csrf)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &r) != nil || r.Created != 3 || r.Links != 3 {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	byName := map[string]int64{}
	list, _ := h.st.Q.ListUsers(ctx)
	for _, u := range list {
		byName[u.Name] = u.ID
	}
	ivan, _ := h.st.Q.GetUser(ctx, byName["ivan_petrov"])
	if !ivan.TrafficLimit.Valid || ivan.TrafficLimit.Int64 != 107374182400 || ivan.UsedDown != 5368709120 || ivan.TotalDown != 21474836480 ||
		!ivan.ExpiresAt.Valid || ivan.ExpiresAt.Int64 != 1893456000 || ivan.Note != "VIP" || ivan.TariffID.Int64 != tariffs[1].ID {
		t.Fatalf("ivan: %+v", ivan)
	}
	olga, _ := h.st.Q.GetUser(ctx, byName["olga"])
	if olga.Status != "disabled" || olga.TrafficLimit.Valid || olga.ExpiresAt.Valid {
		t.Fatalf("olga: %+v", olga)
	}
	pause, _ := h.st.Q.GetUser(ctx, byName["pause"])
	if !pause.ExpiresAt.Valid || pause.ExpiresAt.Int64 != h.now.Unix()+2592000 {
		t.Fatalf("an on-hold term starts at the import: %+v", pause)
	}
	// Again: everyone is there already, nobody is merged.
	resp, body = h.do(http.MethodPost, api+"/import", run, csrf)
	if json.Unmarshal(body, &r) != nil || r.Created != 0 || len(r.Skipped) != 3 {
		t.Fatalf("second import: %d %s", resp.StatusCode, body)
	}

	// Old links: off until the path and the secret are set.
	const secret = "s3cr3t-key-from-jwt-table"
	const ivanToken = "aXZhbl9wZXRyb3YsMTc1OTQwMDAwMAUGNuNGBJks" // Marzban's own code, this secret
	sub := func(path string) int {
		t.Helper()
		resp, _ := h.do(http.MethodGet, path, nil, map[string]string{"User-Agent": "mihomo/1.19.32"})
		return resp.StatusCode
	}
	if sub("/sub/"+ivanToken) != http.StatusNotFound {
		t.Fatal("an old link works without being turned on")
	}
	resp, body = h.do(http.MethodPatch, api+"/import/legacy", map[string]any{"path": adminPath}, csrf)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), "legacy_path_invalid") {
		t.Fatalf("the admin path as the old path: %d %s", resp.StatusCode, body)
	}
	resp, body = h.do(http.MethodPatch, api+"/import/legacy", map[string]any{"path": "/sub/", "kind": "marzban", "secret": secret}, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"secret_set":true`) || strings.Contains(string(body), secret) {
		t.Fatalf("legacy settings: %d %s", resp.StatusCode, body)
	}
	if _, err := h.p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sub("/sub/" + ivanToken); got != http.StatusOK {
		t.Fatalf("ivan's old link: %d", got)
	}
	for _, forged := range []string{
		"/sub/aXZhbl9wZXRyb3YsMTc1OTQwMDAwMAAAAAAAAAAA", // ivan's payload, a guessed signature
		"/sub/name:ivan_petrov",                         // the key the token is filed under
		"/sub/" + ivanToken + "x",
	} {
		if got := sub(forged); got != http.StatusNotFound {
			t.Errorf("%s: %d, a forgery passed", forged, got)
		}
	}
}

func TestImportFromRemnawaveKeepsShortUUIDLinks(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{settings.KeyPublicHost: "203.0.113.10", settings.KeyPanelPort: 21355} {
		if err := settings.Set(ctx, settings.New(h.st.Q), k, v); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatal("login")
	}
	api := "/" + adminPath + "/api/v1"
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	tariffs, _ := h.st.Q.ListTariffs(ctx)
	old := fakeRemnawave(t, []map[string]any{
		{"id": 1, "shortUuid": "Abc123Def456Ghi7", "username": "masha", "status": "ACTIVE", "trafficLimitBytes": 0, "expireAt": "2099-12-31T00:00:00.000Z",
			"telegramId": 777, "hwidDeviceLimit": 2, "userTraffic": map[string]any{"usedTrafficBytes": 100, "lifetimeUsedTrafficBytes": 1000}},
	})
	run := map[string]any{"kind": "remnawave", "url": old.URL, "token": "rw-token", "tariff_id": tariffs[1].ID}
	resp, body := h.do(http.MethodPost, api+"/import", run, csrf)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"created":1`) || !strings.Contains(string(body), `"links":1`) {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	list, _ := h.st.Q.ListUsers(ctx)
	var masha = list[len(list)-1]
	if masha.Name != "masha" || masha.ExpiresAt.Valid || masha.TrafficLimit.Valid || !masha.DeviceLimit.Valid || masha.DeviceLimit.Int64 != 2 || masha.Contact != "tg:777" || masha.TotalDown != 1000 {
		t.Fatalf("masha: %+v", masha)
	}
	if resp, body := h.do(http.MethodPatch, api+"/import/legacy", map[string]any{"path": "api/sub"}, csrf); resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy path: %d %s", resp.StatusCode, body)
	}
	if _, err := h.p.Apply(ctx); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int{
		"/api/sub/Abc123Def456Ghi7":        http.StatusOK,
		"/api/sub/Abc123Def456Ghi7/mihomo": http.StatusOK,
		"/api/sub/Abc123Def456Ghi8":        http.StatusNotFound,
	} {
		resp, body := h.do(http.MethodGet, path, nil, map[string]string{"User-Agent": "Happ/3.4.1"})
		if resp.StatusCode != want {
			t.Errorf("%s: %d %.80s, want %d", path, resp.StatusCode, body, want)
		}
	}
	// The client type after the token picks the format, as in Remnawave.
	if _, body := h.do(http.MethodGet, "/api/sub/Abc123Def456Ghi7/mihomo", nil, map[string]string{"User-Agent": "Happ/3.4.1"}); !strings.Contains(string(body), "proxies") {
		t.Errorf("/mihomo did not give a Clash profile: %.120s", body)
	}
}
