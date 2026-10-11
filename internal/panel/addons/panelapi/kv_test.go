package panelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
	"mikan/internal/panel/store/storetest"
)

// kvWorld is a server with a real database, two approved addons and a clock to move.
func kvWorld(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := storetest.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for id, perms := range map[string]string{"telegram": `["bot","kv"]`, "fresh": `["kv"]`, "nokv": `["bot"]`} {
		if err := st.Q.UpsertAddonGrant(ctx, db.UpsertAddonGrantParams{AddonID: id, Version: "1.0.0", Permissions: perms, ApprovedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	s := state()
	s.Adapters["nokv"] = addons.Installed{Protocol: addons.Protocol2, Kind: addons.KindBot, PanelToken: strings.Repeat("k", 48), Status: "running"}
	writeState(t, dir, s)
	now := time.Unix(1_800_000_000, 0)
	srv := New(Deps{Addons: addons.New(dir, "", "dev", slog.New(slog.DiscardHandler), time.Now), Grants: st.Q, Store: st,
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }})
	return srv, &now
}

type kvResp struct {
	status int
	etag   string
	body   []byte
}

func kv(s *Server, token, method, path string, body []byte, header map[string]string) kvResp {
	r := httptest.NewRequest(method, "http://127.0.0.1:41990/panel/v2/kv"+path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return kvResp{w.Code, w.Header().Get("ETag"), w.Body.Bytes()}
}

func code(r kvResp) string {
	var e Error
	_ = json.Unmarshal(r.body, &e)
	return e.Code
}

// Each addon sees only its own keys; values round-trip; versions make writes conditional.
func TestKV(t *testing.T) {
	s, now := kvWorld(t)
	put := func(token, key, value string, header map[string]string) kvResp {
		return kv(s, token, http.MethodPut, "/"+key, []byte(value), header)
	}

	if r := put(botToken, "chat:555", "menu=12", nil); r.status != 200 || r.etag != `"1"` {
		t.Fatalf("put: %d %s %s", r.status, r.etag, r.body)
	}
	if r := kv(s, botToken, http.MethodGet, "/chat:555", nil, nil); r.status != 200 || string(r.body) != "menu=12" || r.etag != `"1"` {
		t.Fatalf("get: %d %s %s", r.status, r.etag, r.body)
	}
	if r := kv(s, newToken, http.MethodGet, "/chat:555", nil, nil); r.status != 404 {
		t.Fatalf("another addon read it: %d %s", r.status, r.body)
	}
	if r := kv(s, strings.Repeat("k", 48), http.MethodGet, "/chat:555", nil, nil); r.status != 403 || code(r) != "forbidden" {
		t.Fatalf("an addon without kv: %d %s", r.status, r.body)
	}

	// compare-and-set
	if r := put(botToken, "chat:555", "menu=13", map[string]string{"If-Match": `"7"`}); r.status != 409 {
		t.Fatalf("a stale version: %d %s", r.status, r.body)
	}
	if r := put(botToken, "chat:555", "menu=13", map[string]string{"If-Match": `"1"`}); r.status != 200 || r.etag != `"2"` {
		t.Fatalf("the current version: %d %s", r.status, r.body)
	}
	if r := put(botToken, "nothing", "x", map[string]string{"If-Match": `"1"`}); r.status != 409 {
		t.Fatalf("If-Match on no value: %d %s", r.status, r.body)
	}

	// a dedupe mark that expires
	if r := kv(s, botToken, http.MethodPut, "/sent:917?if_absent=1&ttl=60", []byte("1"), nil); r.status != 200 {
		t.Fatalf("mark: %d %s", r.status, r.body)
	}
	if r := kv(s, botToken, http.MethodPut, "/sent:917?if_absent=1&ttl=60", []byte("1"), nil); r.status != 409 || code(r) != "conflict" {
		t.Fatalf("the second mark: %d %s", r.status, r.body)
	}
	*now = now.Add(61 * time.Second)
	if r := kv(s, botToken, http.MethodGet, "/sent:917", nil, nil); r.status != 404 {
		t.Fatalf("an expired value: %d %s", r.status, r.body)
	}
	r := kv(s, botToken, http.MethodPut, "/sent:917?if_absent=1", []byte("1"), nil)
	if r.status != 200 || r.etag != `"2"` {
		t.Fatalf("over an expired value, with the version going on: %d %s %s", r.status, r.etag, r.body)
	}

	// listing
	for _, k := range []string{"user/1", "user/2", "user/3"} {
		put(botToken, k, "v", nil)
	}
	var page struct {
		Keys []struct{ Key string } `json:"keys"`
		Next string                 `json:"next"`
	}
	lr := kv(s, botToken, http.MethodGet, "?prefix=user/&limit=2", nil, nil)
	if err := json.Unmarshal(lr.body, &page); err != nil || len(page.Keys) != 2 || page.Next != "user/2" {
		t.Fatalf("page: %d %s", lr.status, lr.body)
	}
	lr = kv(s, botToken, http.MethodGet, "?prefix=user/&after=user/2", nil, nil)
	if err := json.Unmarshal(lr.body, &page); err != nil || len(page.Keys) != 1 || page.Keys[0].Key != "user/3" || page.Next != "" {
		t.Fatalf("last page: %s", lr.body)
	}

	// delete, conditional and not
	if r := kv(s, botToken, http.MethodDelete, "/user/1", nil, map[string]string{"If-Match": `"9"`}); r.status != 409 {
		t.Fatalf("delete of a stale version: %d", r.status)
	}
	if r := kv(s, botToken, http.MethodDelete, "/user/1", nil, nil); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := kv(s, botToken, http.MethodGet, "/user/1", nil, nil); r.status != 404 {
		t.Fatalf("deleted: %d", r.status)
	}
}

// Keys stay printable, values small, an addon's store bounded.
func TestKVLimits(t *testing.T) {
	s, _ := kvWorld(t)
	for _, k := range []string{"/", "/a%20b", "/" + strings.Repeat("k", 257), "/ключ"} {
		if r := kv(s, newToken, http.MethodPut, k, []byte("v"), nil); r.status != 400 {
			t.Fatalf("key %q: %d %s", k, r.status, r.body)
		}
	}
	if r := kv(s, newToken, http.MethodPut, "/big", bytes.Repeat([]byte("x"), MaxValue+1), nil); r.status != 413 {
		t.Fatalf("a value over the limit: %d", r.status)
	}
	if r := kv(s, newToken, http.MethodPut, "/t?ttl=0", []byte("v"), nil); r.status != 400 {
		t.Fatalf("ttl 0: %d", r.status)
	}
	// No bound by default; a quota set in Deps bounds an addon's store.
	const quota = 1 << 20
	s.d.KVQuota = quota
	value := bytes.Repeat([]byte("x"), MaxValue)
	n := 0
	for ; n < quota/MaxValue+2; n++ {
		r := kv(s, newToken, http.MethodPut, fmt.Sprintf("/fill/%03d", n), value, nil)
		if r.status == http.StatusInsufficientStorage {
			break
		}
		if r.status != 200 {
			t.Fatalf("fill %d: %d %s", n, r.status, r.body)
		}
	}
	if n == 0 || n > quota/MaxValue {
		t.Fatalf("the quota took %d values of 64 KiB", n)
	}
	// rewriting a key in place is not counted twice
	if r := kv(s, newToken, http.MethodPut, "/fill/000", value, nil); r.status != 200 {
		t.Fatalf("a rewrite at the quota: %d %s", r.status, r.body)
	}
	// the admin's setting: 256 MiB when not set, none when set to 0
	s.d.KVQuota = 0
	s.d.Settings = settings.New(s.d.Store.Q)
	if got := s.kvQuota(context.Background()); got != DefaultKVMiB<<20 {
		t.Fatalf("the default quota: %d", got)
	}
	if err := settings.Set(context.Background(), s.d.Settings, KVLimitKey, int64(0)); err != nil {
		t.Fatal(err)
	}
	if r := kv(s, newToken, http.MethodPut, "/more", value, nil); r.status != 200 {
		t.Fatalf("a write with no quota: %d %s", r.status, r.body)
	}
	s.d.KVQuota = quota
	// another addon has its own room
	if r := kv(s, botToken, http.MethodPut, "/x", value, nil); r.status != 200 {
		t.Fatalf("another addon: %d", r.status)
	}
}
