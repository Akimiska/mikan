package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// The marketplace of protocol 2: the admin approves exactly the permissions shown, the grant
// is written before the host is asked, an update asking for more is marked, and removing
// takes the grant back at once. A server whose host predates protocol 2 is refused.
func TestMarketplaceGrants(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	entry := addons.Entry{ID: "telegram", Name: addons.Text{"en": "Bot"}, Version: "1.0.0", Protocol: 2, Kind: addons.KindBot,
		Permissions: []string{"kv", "bot"}, Image: "ghcr.io/getmikan/telegram", Digest: digest}
	var index atomic.Value
	publish := func(entries ...addons.Entry) {
		data, _ := json.Marshal(addons.Catalog{Version: 1, Adapters: entries})
		index.Store(data)
	}
	publish(entry, addons.Entry{ID: "fake", Version: "1.0.0", Protocol: 1, Image: "ghcr.io/getmikan/adapter-fake", Digest: digest})
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := index.Load().([]byte)
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(data)
		case "/index.json.sig":
			_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(catalog.Close)

	dir := t.TempDir()
	h := newHarness(t, func(o *Options) { o.DataDir, o.AddonsCatalog = dir, catalog.URL+"/index.json" })
	h.p.Addons.SetKey(pub)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1/marketplace"
	type view struct {
		Catalog []struct {
			ID          string   `json:"id"`
			Permissions []string `json:"permissions"`
			Installed   bool     `json:"installed"`
			Wider       bool     `json:"wider"`
		} `json:"catalog"`
		Installed []struct {
			ID      string   `json:"id"`
			Granted []string `json:"granted"`
		} `json:"installed"`
		HostReady bool `json:"host_ready"`
	}
	read := func(resp *http.Response, body []byte) view {
		t.Helper()
		var v view
		if resp.StatusCode/100 != 2 || json.Unmarshal(body, &v) != nil {
			t.Fatalf("marketplace: %d %s", resp.StatusCode, body)
		}
		return v
	}
	host := func(st addons.State) {
		t.Helper()
		data, _ := json.Marshal(st)
		if err := os.MkdirAll(filepath.Join(dir, "addons"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "addons", "state.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	approve := func(perms ...string) map[string]any {
		return map[string]any{"permissions": append([]string{}, perms...)}
	}
	grant := func() (string, bool) {
		g, err := h.st.Q.GetAddonGrant(context.Background(), "telegram")
		if errors.Is(err, sql.ErrNoRows) {
			return "", false
		}
		if err != nil {
			t.Fatal(err)
		}
		return g.Permissions, true
	}

	// Only protocol 2 shows here; the payment adapter stays with payments.
	v := read(h.do(http.MethodGet, api, nil, nil))
	if len(v.Catalog) != 1 || v.Catalog[0].ID != "telegram" || v.HostReady {
		t.Fatalf("catalog: %+v", v)
	}
	if resp, body := h.do(http.MethodPost, api+"/telegram/install", approve("bot", "kv"), csrf); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "host_outdated") {
		t.Fatalf("a host before protocol 2: %d %s", resp.StatusCode, body)
	}
	host(addons.State{Adapters: map[string]addons.Installed{}, HostVersion: "0.5.1.0"})
	if resp, body := h.do(http.MethodPost, api+"/fake/install", approve(), csrf); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a payment adapter through this marketplace: %d %s", resp.StatusCode, body)
	}
	for _, perms := range [][]string{{"bot"}, {"bot", "kv", "users:write"}, nil} {
		if resp, body := h.do(http.MethodPost, api+"/telegram/install", approve(perms...), csrf); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "permissions_changed") {
			t.Fatalf("approving %v: %d %s", perms, resp.StatusCode, body)
		}
	}
	if _, ok := grant(); ok {
		t.Fatal("a refused approval left a grant")
	}
	read(h.do(http.MethodPost, api+"/telegram/install", approve("kv", "bot"), csrf))
	if g, ok := grant(); !ok || g != `["bot","kv"]` {
		t.Fatalf("grant: %q %v", g, ok)
	}
	if q := h.p.Addons.Pending(); len(q) != 1 || q[0].Action != "install" || q[0].ID != "telegram" {
		t.Fatalf("the host is not asked: %+v", q)
	}

	// The host ran it; the next build asks for more.
	if err := os.RemoveAll(filepath.Join(dir, "addons", "requests")); err != nil {
		t.Fatal(err)
	}
	host(addons.State{HostVersion: "0.5.1.0", Adapters: map[string]addons.Installed{"telegram": {Version: "1.0.0", Digest: digest,
		Protocol: 2, Kind: addons.KindBot, Status: "running"}}})
	wider := entry
	wider.Version, wider.Digest, wider.Permissions = "1.1.0", "sha256:"+strings.Repeat("c", 64), []string{"bot", "kv", "users:write"}
	publish(wider)
	h.now = h.now.Add(11 * time.Minute) // past the cached catalog
	v = read(h.do(http.MethodGet, api, nil, nil))
	if len(v.Installed) != 1 || strings.Join(v.Installed[0].Granted, ",") != "bot,kv" {
		t.Fatalf("installed: %+v", v.Installed)
	}
	if len(v.Catalog) != 1 || !v.Catalog[0].Wider || !v.Catalog[0].Installed {
		t.Fatalf("an update asking for more is not marked: %+v", v.Catalog)
	}

	// A grant key of the addon is its own: removing the addon takes it too, so one installed
	// later under the id does not get the old one's results.
	ctx := context.Background()
	if err := domain.Seed(ctx, h.st, h.now); err != nil {
		t.Fatal(err)
	}
	tariffs, _ := h.st.Q.ListTariffs(ctx)
	clock := func() time.Time { return h.now }
	u, err := domain.NewUsers(h.st, domain.NewPool(h.st, clock), noChanges{}, clock).Create(ctx, domain.CreateInput{Name: "a", TariffID: tariffs[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"telegram", "neighbour"} {
		if n, err := h.st.Q.ClaimAddonGrantKey(ctx, db.ClaimAddonGrantKeyParams{AddonID: id, Key: "k", UserID: u.ID, CreatedAt: 1}); err != nil || n != 1 {
			t.Fatalf("claim: %d %v", n, err)
		}
	}
	if resp, body := h.do(http.MethodPost, api+"/telegram/remove", nil, csrf); resp.StatusCode/100 != 2 {
		t.Fatalf("remove: %d %s", resp.StatusCode, body)
	}
	if _, ok := grant(); ok {
		t.Fatal("the grant outlived the removal")
	}
	if _, err := h.st.Q.GetAddonGrantKey(ctx, db.GetAddonGrantKeyParams{AddonID: "telegram", Key: "k"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("the grant key outlived the removal: %v", err)
	}
	if _, err := h.st.Q.GetAddonGrantKey(ctx, db.GetAddonGrantKeyParams{AddonID: "neighbour", Key: "k"}); err != nil {
		t.Fatalf("another addon's key went with it: %v", err)
	}
}

// An addon of the admin's own: an image by its digest or a loopback process. The request is
// checked field by field before anything is written, the grant comes before the host is
// asked, an id of the catalog or of another source is refused, and only a session may do it.
func TestMarketplaceCustomAddons(t *testing.T) {
	digest := "sha256:" + strings.Repeat("d", 64)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	data, _ := json.Marshal(addons.Catalog{Version: 1, Adapters: []addons.Entry{
		{ID: "telegram", Version: "1.0.0", Protocol: 2, Kind: addons.KindBot, Permissions: []string{"bot"}, Image: "ghcr.io/getmikan/telegram", Digest: digest},
		{ID: "yookassa", Version: "1.0.0", Protocol: 1, Image: "ghcr.io/getmikan/adapter-yookassa", Digest: digest},
	}})
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(data)
		case "/index.json.sig":
			_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(catalog.Close)

	dir := t.TempDir()
	h := newHarness(t, func(o *Options) { o.DataDir, o.AddonsCatalog = dir, catalog.URL+"/index.json" })
	h.p.Addons.SetKey(pub)
	if resp, _ := h.login(password, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	csrf := map[string]string{"X-CSRF-Token": h.csrf}
	api := "/" + adminPath + "/api/v1/marketplace"
	host := func(st addons.State) {
		t.Helper()
		data, _ := json.Marshal(st)
		if err := os.MkdirAll(filepath.Join(dir, "addons"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "addons", "state.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	custom := func(edit func(b map[string]any)) map[string]any {
		b := map[string]any{"id": "mybot", "source": "custom", "image": "ghcr.io/me/mybot", "digest": digest, "kind": "bot", "mode": "client",
			"permissions": []string{"kv", "bot"}, "mem_mb": 96, "confirm": true}
		if edit != nil {
			edit(b)
		}
		return b
	}
	refused := func(what string, body map[string]any, status int, codes ...string) {
		t.Helper()
		resp, out := h.do(http.MethodPost, api+"/custom", body, csrf)
		if resp.StatusCode != status {
			t.Fatalf("%s: %d %s, want %d", what, resp.StatusCode, out, status)
		}
		for _, c := range codes {
			if !strings.Contains(string(out), c) {
				t.Fatalf("%s: %s, want %s", what, out, c)
			}
		}
	}
	grant := func(id string) (db.AddonGrant, bool) {
		g, err := h.st.Q.GetAddonGrant(context.Background(), id)
		if errors.Is(err, sql.ErrNoRows) {
			return g, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return g, true
	}

	// An API key never runs code on the server, however full.
	resp, body := h.do(http.MethodPost, "/"+adminPath+"/api/v1/api-keys", map[string]any{"name": "full", "scope": "full", "password": password}, csrf)
	var key struct {
		Key string `json:"key"`
	}
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &key) != nil {
		t.Fatalf("make a key: %d %s", resp.StatusCode, body)
	}
	jar := h.client.Jar
	h.client.Jar = nil
	resp, body = h.do(http.MethodPost, api+"/custom", custom(nil), map[string]string{"Authorization": "Bearer " + key.Key})
	h.client.Jar = jar
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "session_only") {
		t.Fatalf("a full key installs code: %d %s", resp.StatusCode, body)
	}

	refused("a host before protocol 2", custom(nil), http.StatusConflict, "host_outdated")
	host(addons.State{Adapters: map[string]addons.Installed{}, HostVersion: "0.5.1.0"})
	refused("not confirmed", custom(func(b map[string]any) { b["confirm"] = false }), http.StatusUnprocessableEntity, "addon_not_confirmed")
	refused("a tag instead of a digest", custom(func(b map[string]any) { b["image"], b["digest"] = "ghcr.io/me/mybot:latest", "" }),
		http.StatusUnprocessableEntity, "addon_image_invalid", "addon_digest_invalid")
	refused("a tag beside the digest", custom(func(b map[string]any) { b["image"] = "ghcr.io/me/mybot:1.0" }), http.StatusUnprocessableEntity, "addon_image_invalid")
	refused("an unknown permission", custom(func(b map[string]any) { b["permissions"] = []string{"bot", "root"} }), http.StatusUnprocessableEntity, "addon_permissions_invalid")
	refused("too much memory", custom(func(b map[string]any) { b["mem_mb"] = 1024 }), http.StatusUnprocessableEntity, "addon_mem_invalid")
	refused("an id of the catalog", custom(func(b map[string]any) { b["id"] = "telegram" }), http.StatusUnprocessableEntity, "addon_id_taken")
	refused("an id of a payment adapter", custom(func(b map[string]any) { b["id"] = "yookassa" }), http.StatusUnprocessableEntity, "addon_id_taken")
	for _, url := range []string{"http://10.0.0.5:8080", "http://localhost:8080", "https://127.0.0.1:8080", "http://127.0.0.1:80", "http://0.0.0.0:8080"} {
		refused("external at "+url, map[string]any{"id": "ext", "source": "external", "url": url, "kind": "generic", "mode": "http", "permissions": []string{}, "confirm": true},
			http.StatusUnprocessableEntity, "addon_url_invalid")
	}
	refused("external serving nothing", map[string]any{"id": "ext", "source": "external", "kind": "generic", "mode": "http", "permissions": []string{}, "confirm": true},
		http.StatusUnprocessableEntity, "addon_url_invalid")
	for _, id := range []string{"mybot", "telegram", "yookassa", "ext"} {
		if _, ok := grant(id); ok {
			t.Fatalf("a refused request left a grant for %s", id)
		}
	}
	if q := h.p.Addons.Pending(); len(q) != 0 {
		t.Fatalf("a refused request reached the host: %+v", q)
	}

	if resp, body := h.do(http.MethodPost, api+"/custom", custom(nil), csrf); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("custom install: %d %s", resp.StatusCode, body)
	}
	if g, ok := grant("mybot"); !ok || g.Version != "custom" || g.Permissions != `["bot","kv"]` {
		t.Fatalf("grant: %+v %v", g, ok)
	}
	q := h.p.Addons.Pending()
	if len(q) != 1 || q[0].ID != "mybot" || q[0].Source != "custom" || q[0].Image != "ghcr.io/me/mybot" || q[0].Digest != digest ||
		q[0].Kind != "bot" || q[0].Mode != "client" || q[0].MemMB != 96 || strings.Join(q[0].Permissions, ",") != "kv,bot" || q[0].URL != "" {
		t.Fatalf("the host's request: %+v", q)
	}
	refused("again while queued", custom(nil), http.StatusConflict, "addon_busy")
	var audit string
	if err := h.st.DB.QueryRow("SELECT details FROM audit_log WHERE action = 'addon.install' ORDER BY id DESC LIMIT 1").Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(audit, `"source": "custom"`) && !strings.Contains(audit, `"source":"custom"`) || !strings.Contains(audit, "ghcr.io/me/mybot") {
		t.Fatalf("audit: %s", audit)
	}
	// A client of the panel needs no address of its own.
	if resp, body := h.do(http.MethodPost, api+"/custom", map[string]any{"id": "ext", "source": "external", "kind": "generic", "mode": "client",
		"permissions": []string{"kv"}, "confirm": true}, csrf); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("external client: %d %s", resp.StatusCode, body)
	}

	// The host ran them: the list says where they come from; the catalog's id stays the
	// catalog's even if an addon of the admin's holds it (a catalog that took it later).
	if err := os.RemoveAll(filepath.Join(dir, "addons", "requests")); err != nil {
		t.Fatal(err)
	}
	host(addons.State{HostVersion: "0.5.1.0", Adapters: map[string]addons.Installed{
		"mybot":    {Image: "ghcr.io/me/mybot", Digest: digest, Protocol: 2, Source: "custom", Kind: "bot", Mode: "client", Status: "running"},
		"ext":      {Listen: "127.0.0.1:45000", Protocol: 2, Source: "external", Kind: "generic", Status: "running"},
		"telegram": {Image: "ghcr.io/me/fake", Digest: digest, Protocol: 2, Source: "custom", Kind: "bot", Status: "running"},
	}})
	var v struct {
		Catalog []struct {
			ID        string `json:"id"`
			Installed bool   `json:"installed"`
		} `json:"catalog"`
		Installed []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
			Image  string `json:"image"`
			Digest string `json:"digest"`
			URL    string `json:"url"`
			Mode   string `json:"mode"`
		} `json:"installed"`
	}
	resp, body = h.do(http.MethodGet, api, nil, nil)
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &v) != nil {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if len(v.Installed) != 3 || v.Installed[0].ID != "ext" || v.Installed[0].Source != "external" || v.Installed[0].URL != "http://127.0.0.1:45000" || v.Installed[0].Mode != "http" ||
		v.Installed[1].ID != "mybot" || v.Installed[1].Source != "custom" || v.Installed[1].Image != "ghcr.io/me/mybot" || v.Installed[1].Digest != digest {
		t.Fatalf("installed: %+v", v.Installed)
	}
	if len(v.Catalog) != 1 || v.Catalog[0].Installed {
		t.Fatalf("an addon of the admin's passes for the catalog's: %+v", v.Catalog)
	}
	if resp, body := h.do(http.MethodPost, api+"/telegram/install", map[string]any{"permissions": []string{"bot"}}, csrf); resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "addon_id_taken") {
		t.Fatalf("the catalog's over the admin's: %d %s", resp.StatusCode, body)
	}
	refused("the other source under the same id", map[string]any{"id": "mybot", "source": "external", "url": "http://127.0.0.1:46000", "kind": "bot", "mode": "http",
		"permissions": []string{}, "confirm": true}, http.StatusUnprocessableEntity, "addon_id_taken")

	// Removing goes the way of the catalog's: the grant first.
	if resp, body := h.do(http.MethodPost, api+"/mybot/remove", nil, csrf); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("remove: %d %s", resp.StatusCode, body)
	}
	if _, ok := grant("mybot"); ok {
		t.Fatal("the grant outlived the removal")
	}
	if q := h.p.Addons.Pending(); len(q) != 1 || q[0].Action != "remove" || q[0].ID != "mybot" {
		t.Fatalf("remove request: %+v", q)
	}
}
