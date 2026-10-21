package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The API lock is a remote node's: the panel's own node, reached over its socket, refuses
// it; the networks added are checked and written one way.
func TestNodeAPILockdown(t *testing.T) {
	f := newHostFixture(t, false, func(d *Deps) { d.Nodes = nil })
	if code, body := f.send(http.MethodPatch, "/api/v1/nodes/1", `{"api_lockdown": true}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "local_node_lockdown") {
		t.Fatalf("the panel's own node took the lock: %d %s", code, body)
	}
	if code, body := f.send(http.MethodPatch, "/api/v1/nodes/1", `{"api_allow": ["203.0.113.5"]}`); code != http.StatusUnprocessableEntity || !strings.Contains(body, "local_node_lockdown") {
		t.Fatalf("the panel's own node took networks: %d %s", code, body)
	}
	if code, body := f.send(http.MethodPatch, "/api/v1/nodes/1", `{"api_lockdown": false}`); code != http.StatusOK {
		t.Fatalf("off is fine for the panel's own node too: %d %s", code, body)
	}
	for _, bad := range []string{`["nope"]`, `["0.0.0.0/0"]`, `["203.0.113.5/33"]`, `["fe80::1%eth0"]`} {
		code, body := f.send(http.MethodPatch, "/api/v1/nodes/2", `{"api_allow": `+bad+`}`)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "api_allow_invalid") || !strings.Contains(body, "body.api_allow") {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	many := make([]string, 17)
	for i := range many {
		many[i] = "203.0.113." + strconv.Itoa(i+1)
	}
	raw, _ := json.Marshal(map[string]any{"api_allow": many})
	if code, body := f.send(http.MethodPatch, "/api/v1/nodes/2", string(raw)); code != http.StatusUnprocessableEntity {
		t.Fatalf("17 networks were taken: %d %s", code, body)
	}
	if n, _ := f.st.GetNode(context.Background(), 2); n.ApiLockdown != 0 || len(n.ApiAllow) != 0 {
		t.Fatalf("a refused change was written: %+v", n)
	}

	audited := f.audited("node.update")
	code, body := f.send(http.MethodPatch, "/api/v1/nodes/2", `{"api_lockdown": true, "api_allow": [" 203.0.113.9 ", "198.51.100.7/24", "203.0.113.9", ""]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got NodeInfo
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"198.51.100.0/24", "203.0.113.9/32"}
	if !got.APILockdown || !slices.Equal(got.APIAllow, want) {
		t.Fatalf("the node: lock %v, networks %v", got.APILockdown, got.APIAllow)
	}
	if n, _ := f.st.GetNode(context.Background(), 2); n.ApiLockdown != 1 || !slices.Equal(n.ApiAllow, want) {
		t.Fatalf("the row: %+v", n)
	}
	if f.audited("node.update") != audited+1 {
		t.Fatal("the change is not in the journal")
	}
	// Each field alone keeps the other.
	if code, body := f.send(http.MethodPatch, "/api/v1/nodes/2", `{"api_lockdown": false}`); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if n, _ := f.st.GetNode(context.Background(), 2); n.ApiLockdown != 0 || !slices.Equal(n.ApiAllow, want) {
		t.Fatalf("turning the lock off dropped the networks: %+v", n)
	}
}
