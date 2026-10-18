package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"mikan/internal/proto"
)

// A copy has the original's settings with keys of its own, a port of its own when the
// original's is taken, and is refused on a port another inbound holds.
func TestCopyInbound(t *testing.T) {
	k := newKeyHarness(t)
	ins, err := k.st.Q.ListInbounds(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var src struct {
		id     int64
		config string
		port   string
	}
	for _, in := range ins {
		if in.Name == "vless-xhttp" {
			src.id, src.config, src.port = in.ID, in.Config, in.Port
		}
	}
	if src.id == 0 {
		t.Fatal("no seeded vless-xhttp")
	}
	var v struct {
		Name   string `json:"name"`
		Port   string `json:"port"`
		Config string `json:"config"`
	}
	resp, raw := k.do(http.MethodPost, k.api+"/inbounds/"+strconv.FormatInt(src.id, 10)+"/copy", map[string]any{}, k.csrf)
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(raw, &v) != nil {
		t.Fatalf("copy: %d %s", resp.StatusCode, raw)
	}
	if v.Name == "vless-xhttp" || v.Port == src.port {
		t.Fatalf("the copy needs a name and a port of its own: %+v", v)
	}
	orig, _ := proto.Parse(src.config)
	cp, err := proto.Parse(v.Config)
	if err != nil {
		t.Fatal(err)
	}
	ro, rc := orig["reality-config"].(map[string]any), cp["reality-config"].(map[string]any)
	if ro["private-key"] == rc["private-key"] || ro["dest"] != rc["dest"] {
		t.Fatalf("keys of its own, the same target: %v / %v", ro, rc)
	}
	if orig["xhttp-config"].(map[string]any)["path"] != cp["xhttp-config"].(map[string]any)["path"] {
		t.Fatal("the settings are copied")
	}

	// Another target on the way, and a port another inbound holds is refused.
	resp, raw = k.do(http.MethodPost, k.api+"/inbounds/"+strconv.FormatInt(src.id, 10)+"/copy", map[string]any{"port": v.Port}, k.csrf)
	if resp.StatusCode < 400 || !strings.Contains(string(raw), "port") {
		t.Fatalf("a taken port: %d %s", resp.StatusCode, raw)
	}
	resp, raw = k.do(http.MethodPost, k.api+"/inbounds/999999/copy", map[string]any{}, k.csrf)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no such inbound: %d %s", resp.StatusCode, raw)
	}
}
