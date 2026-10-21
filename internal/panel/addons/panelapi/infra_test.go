package panelapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/addons"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

var (
	viewerToken = strings.Repeat("v", 48)
	tunerToken  = strings.Repeat("u", 48)
)

// nodes:read and inbounds:read see the servers without keys; inbounds:write turns a protocol
// off and moves its port, audited under the addon's name; nobody else gets in.
func TestInfra(t *testing.T) {
	st := state()
	for id, tok := range map[string]string{"viewer": viewerToken, "tuner": tunerToken, "reader": readerToken} {
		st.Adapters[id] = addons.Installed{Protocol: addons.Protocol2, Kind: addons.KindGeneric, PanelToken: tok, Token: "t", Status: "running"}
	}
	srv, _ := world(t, st, grants{
		"viewer": {Version: "1", Permissions: `["nodes:read","inbounds:read"]`},
		"tuner":  {Version: "1", Permissions: `["inbounds:read","inbounds:write"]`},
		"reader": {Version: "1", Permissions: `["users:all"]`},
	})
	now := time.Unix(1_800_000_000, 0)
	srv.d.Now = func() time.Time { return now }
	ctx := context.Background()
	if err := domain.Seed(ctx, srv.d.Store, now); err != nil {
		t.Fatal(err)
	}
	ch := &changeCount{}
	srv.d.Inbounds = domain.NewInbounds(srv.d.Store, nil, srv.d.Now)
	srv.d.Changes = ch
	srv.d.NodeState = func(int64) NodeState { return NodeState{Known: true, OK: true, Version: "0.5.1.1", Conns: 3} }
	srv.handleInfra()
	do := func(token, method, path, body string) (int, string) {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:41990/panel/v2"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	code, body := do(viewerToken, http.MethodGet, "/nodes", "")
	var nodes []nodeView
	if code != http.StatusOK || json.Unmarshal([]byte(body), &nodes) != nil || len(nodes) != 1 || !nodes[0].Local || nodes[0].Status != "ok" || nodes[0].Conns != 3 {
		t.Fatalf("nodes: %d %s", code, body)
	}
	code, body = do(viewerToken, http.MethodGet, "/inbounds", "")
	var ins []inboundView
	if code != http.StatusOK || json.Unmarshal([]byte(body), &ins) != nil || len(ins) == 0 {
		t.Fatalf("inbounds: %d %s", code, body)
	}
	for _, secret := range []string{"private-key", "uuid", "password", "short-id"} {
		if strings.Contains(body, secret) {
			t.Fatalf("the inbounds show %s: %s", secret, body)
		}
	}
	if code, _ := do(viewerToken, http.MethodPatch, fmt.Sprintf("/inbounds/%d", ins[0].ID), `{"enabled":false}`); code != http.StatusForbidden {
		t.Fatalf("a viewer changed an inbound: %d", code)
	}
	if code, _ := do(tunerToken, http.MethodGet, "/nodes", ""); code != http.StatusForbidden {
		t.Fatalf("nodes without nodes:read: %d", code)
	}
	if code, _ := do(readerToken, http.MethodGet, "/inbounds", ""); code != http.StatusForbidden {
		t.Fatalf("inbounds without inbounds:read: %d", code)
	}

	path := fmt.Sprintf("/inbounds/%d", ins[0].ID)
	if code, body := do(tunerToken, http.MethodPatch, path, `{"enabled":false,"port":"40443","reason":"blocked"}`); code != http.StatusOK || !strings.Contains(body, `"port":"40443"`) || !strings.Contains(body, `"status":"off"`) {
		t.Fatalf("patch: %d %s", code, body)
	}
	row, _ := srv.d.Store.Q.GetInbound(ctx, ins[0].ID)
	if row.Enabled != 0 || row.Port != "40443" {
		t.Fatalf("not changed: %+v", row)
	}
	if ch.n.Load() != 0 {
		t.Fatal("policies are not what changed")
	}
	logs, _ := srv.d.Store.Q.ListAudit(ctx, db.ListAuditParams{BeforeID: 1 << 62, Lim: 100})
	if len(logs) == 0 || logs[0].Action != "inbound.update" || !strings.Contains(logs[0].Details.String, `"addon:tuner"`) || !strings.Contains(logs[0].Details.String, `"blocked"`) {
		t.Fatalf("audit: %+v", logs)
	}
	for _, c := range []struct{ body, code string }{
		{`{}`, "bad_request"},
		{`{"port":"70000"}`, "bad_request"},
		{`{"config":"x"}`, "bad_request"},
	} {
		if code, body := do(tunerToken, http.MethodPatch, path, c.body); code != http.StatusBadRequest || !strings.Contains(body, c.code) {
			t.Errorf("%s: %d %s", c.body, code, body)
		}
	}
	if code, _ := do(tunerToken, http.MethodPatch, "/inbounds/99999", `{"enabled":true}`); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}
}
