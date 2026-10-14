package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"mikan/internal/panel/store/db"
	"mikan/internal/panel/support"
)

func TestMaskPeople(t *testing.T) {
	in := `{"items":[{"id":7,"name":"Ivan Petrov","contact":"@ivan","note":"owes","sub_url":"https://h/s/tok","online_ips":["1.2.3.4"],"used_up":123456789012345678,` +
		`"telegram":{"id":99,"username":"ivan_tg","name":"Ivan"},"legacy":{"url":"https://old/tok"},"tags":["vip"]}],` +
		`"top_month":[{"id":3,"name":"Anna","bytes":5}],` +
		`"payment":{"user_id":7,"user_name":"Ivan Petrov","tg_id":99,"tg_username":"ivan_tg","tariff_name":"Month <b>"},` +
		`"hit":{"user_id":7,"ip":"5.6.7.8","user_name":"Ivan Petrov","node_name":"DE-1"},` +
		`"device":{"id":1,"name":"Ivan's iPhone","hwid":"abc","last_ip":"9.9.9.9","platform":"ios"},` +
		`"node":{"id":1,"name":"DE-1","ip":"203.0.113.9","note":"fine"},"tariff":{"id":2,"name":"Month"}}`
	out := string(maskPeople([]byte(in)))
	for _, leak := range []string{"Ivan", "@ivan", "owes", "tok", "1.2.3.4", "99", "ivan_tg", "old", "Anna", "5.6.7.8", "abc", "9.9.9.9"} {
		// 99 also stands in "tg_id":99 only; used_up has no 99 run.
		if strings.Contains(out, leak) {
			t.Errorf("%q survives: %s", leak, out)
		}
	}
	for _, keep := range []string{`"name":"#7"`, `"name":"#3"`, `"user_name":"#7"`, `"tg_id":0`, `"used_up":123456789012345678`, `"tags":["vip"]`, `"online_ips":[]`,
		`"tariff_name":"Month <b>"`, `"node_name":"DE-1"`, `"ip":"203.0.113.9"`, `"name":"DE-1"`, `"name":"Month"`, `"platform":"ios"`, `"bytes":5`} {
		if !strings.Contains(out, keep) {
			t.Errorf("%s is lost: %s", keep, out)
		}
	}
	// What is not an object of people passes untouched, and what is not JSON is not sent.
	if got := string(maskPeople([]byte(`[1,"a",null,true]`))); got != `[1,"a",null,true]` {
		t.Errorf("scalars: %s", got)
	}
	if got := string(maskPeople([]byte(`<html>`))); !strings.Contains(got, "masking_failed") {
		t.Errorf("garbage: %s", got)
	}
}

// The writer holds an answer only for a caller marked for masking, and sends the others
// as they were written.
func TestMaskResponses(t *testing.T) {
	body := `{"name":"Ivan","contact":"@ivan","sub_url":"x"}`
	handler := func(mark bool) http.Handler {
		return maskResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mark {
				r.Context().Value(keyMask).(*maskState).on.Store(true)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "999")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
	}
	plain := httptest.NewRecorder()
	handler(false).ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/", nil))
	if plain.Code != http.StatusCreated || plain.Body.String() != body {
		t.Fatalf("unmarked: %d %s", plain.Code, plain.Body)
	}
	masked := httptest.NewRecorder()
	handler(true).ServeHTTP(masked, httptest.NewRequest(http.MethodGet, "/", nil))
	if masked.Code != http.StatusCreated || strings.Contains(masked.Body.String(), "Ivan") || strings.Contains(masked.Body.String(), "@ivan") {
		t.Fatalf("marked: %d %s", masked.Code, masked.Body)
	}
	if masked.Header().Get("Content-Length") == "999" || masked.Header().Get("Content-Length") == "" {
		t.Fatalf("the length of the held answer is stale: %q", masked.Header().Get("Content-Length"))
	}
}

func TestSupportGate(t *testing.T) {
	op := func(id string, tags ...string) *huma.Operation { return &huma.Operation{OperationID: id, Tags: tags} }
	view, change, terminal := db.SupportGrant{Level: support.LevelView}, db.SupportGrant{Level: support.LevelChange}, db.SupportGrant{Level: support.LevelTerminal}
	for _, c := range []struct {
		op       *huma.Operation
		mutating bool
		g        db.SupportGrant
		status   int
		code     string
	}{
		{op("list-users", "users"), false, view, 0, ""},
		{op("update-user", "users"), true, view, 403, "support_read_only"},
		{op("update-user", "users"), true, change, 0, ""},
		{op("update-user", "users"), true, terminal, 0, ""},
		{op("me", "auth"), false, change, 403, "support_denied"},
		{op("list-api-keys", "api-keys"), false, view, 403, "support_denied"},
		{op("create-support-grant", "support"), true, change, 403, "support_denied"},
		{op("list-audit", "settings"), false, view, 403, "support_denied"},
		{op("reset-admin-path", "settings"), true, change, 403, "support_denied"},
		// Closed before read-only: the answer for a write to a closed endpoint says closed.
		{op("reset-admin-path", "settings"), true, view, 403, "support_denied"},
		{op("support-whoami", supportOperatorTag), false, view, 0, ""},
	} {
		if status, code := supportGate(c.op, c.mutating, c.g); status != c.status || code != c.code {
			t.Errorf("%s mutating=%v level=%s: %d %s, want %d %s", c.op.OperationID, c.mutating, c.g.Level, status, code, c.status, c.code)
		}
	}
}

func TestSupportTerminalAllowed(t *testing.T) {
	grant := func(level string, targets ...string) context.Context {
		return context.WithValue(context.Background(), keySupport, supportCtx{grant: db.SupportGrant{Level: level, TerminalTargets: targets}, via: "api"})
	}
	for _, c := range []struct {
		name   string
		ctx    context.Context
		target string
		want   bool
	}{
		{"terminal on the panel", grant("terminal", "panel"), "panel", true},
		{"terminal on a named node", grant("terminal", "panel", "node:3"), "node:3", true},
		{"terminal on a node not named", grant("terminal", "panel", "node:3"), "node:4", false},
		{"change with targets in the row", grant("change", "panel"), "panel", false},
		{"view", grant("view"), "panel", false},
		{"empty target", grant("terminal", "panel"), "", false},
		{"the admin's own session", context.Background(), "panel", false},
		{"an API key", context.WithValue(context.Background(), keyAPIKey, db.ApiKey{Scope: "full"}), "panel", false},
	} {
		if got := supportTerminalAllowed(c.ctx, c.target); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
