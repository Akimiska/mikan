package logbuf

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// Every kind of secret the panel and the node handle must be gone from the line, and what is
// around it must stay: a log with nothing readable in it helps nobody.
func TestRedactCutsSecrets(t *testing.T) {
	tok := "AbCdEfGhIjKlMnOpQrStUv0123456789"
	cases := []struct{ name, in, secret, keep string }{
		{"support token", "support enter ms_" + tok + " ok", "ms_" + tok, "support enter"},
		{"api key", "key mk_" + tok + " used", "mk_" + tok, "used"},
		{"bearer header", "Authorization: Bearer " + tok, tok, "Authorization"},
		{"bearer in text", "got bearer " + tok + " from 1.2.3.4", tok, "1.2.3.4"},
		{"token field", `msg="login" token=` + tok + ` ip=1.2.3.4`, tok, "ip=1.2.3.4"},
		{"quoted password", `err="bad" password="hunter 2 hunter" user=root`, "hunter", "user=root"},
		{"cookie", "cookie=mikan_session=" + tok, tok, "cookie="},
		{"secret in JSON", `{"api_key":"` + tok + `","name":"x"}`, tok, `"name":"x"`},
		{"JSON in a slog value", `body="{\"token\":\"` + tok + `\"}"`, tok, "body="},
		{"vless link", "link vless://2f1b9f0e-1111-4222-8333-444455556666@host:443?security=reality&pbk=" + tok + "#name", tok, "link vless://"},
		{"hysteria link", "hy2://" + tok + "@host:443/?obfs=salamander", tok, "hy2://"},
		{"telegram token", "GET https://api.telegram.org/bot123456789:" + tok + "/getUpdates", tok, "getUpdates"},
		{"url password", "dial https://admin:hunter2pass@host:8080/x", "hunter2pass", "host:8080"},
		{"uuid", "user 2f1b9f0e-1111-4222-8333-444455556666 joined", "2f1b9f0e-1111-4222-8333-444455556666", "joined"},
		{"subscription link", "GET https://panel.example.com:8443/Ab12Cd34Ef56/Zx" + tok + " 200", tok, "panel.example.com"},
		{"admin path", "request path=/" + tok + "/api/v1/users", tok, "/api/v1/users"},
		{"wireguard key", "warp private " + strings.Repeat("A", 43) + "= done", strings.Repeat("A", 43), "done"},
		{"jwt", "id_token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk x", "eyJzdWIi", "x"},
		{"bare key", "fix: mikan join eyAbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-AbCdEfGhIjKl now", "AbCdEfGhIjKl", "fix: mikan join"},
		{"private key", "key -----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY----- end", "MIIEvQ", " end"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: the secret is still there: %q", c.name, got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("%s: too much is gone, %q is missing: %q", c.name, c.keep, got)
		}
	}
}

// Ordinary lines are not touched: file names, unit names, versions, ports.
func TestRedactLeavesOrdinaryLines(t *testing.T) {
	for _, line := range []string{
		`time=2026-10-14T10:00:00Z level=INFO msg="panel started" listen=0.0.0.0:21355 tls=true version=0.5.1.1`,
		"msg=update unit=mikan-update-request.service path=/opt/mikan/data/panel/update/request",
		"image ghcr.io/miroshka000/mikan-panel:0.5.1.0 pulled",
		"node 4 health ok users=120 inbounds=3",
		"image ghcr.io/miroshka000/mikan-panel@sha256:0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0 runs",
		"unit mikan-update-request-and-a-very-long-name-of-letters-only started",
	} {
		if got := Redact(line); got != line {
			t.Errorf("changed an ordinary line:\n%s\n%s", line, got)
		}
	}
}

// What names a subscriber goes for a reader who may not see the subscribers; the rest of the
// line, versions and times stay.
func TestRedactPeople(t *testing.T) {
	cases := []struct{ name, in, secret, keep string }{
		{"tg id", "billing: free plan tg=123456789 user=5", "123456789", "user=5"},
		{"tg id field", "bot: linked tg_id=987654321 ok", "987654321", "ok"},
		{"ip field", "billing: adapter refused a webhook ip=1.2.3.4", "1.2.3.4", "refused a webhook"},
		{"client ip", `msg="denied" client_ip="203.0.113.77" code=403`, "203.0.113.77", "code=403"},
		{"bare ipv4", "dial from 198.51.100.7:5555 failed", "198.51.100.7", "failed"},
		{"ipv6", "accepted 2001:db8::1 now", "2001:db8::1", "accepted"},
		{"loopback v6", "from ::1 ok", "::1", "ok"},
		{"hwid", "device hwid=abcdef0123 bound", "abcdef0123", "bound"},
		{"json", `body="{\"tg_id\":4242424,\"plan\":\"m\"}"`, "4242424", `plan`},
		{"username", "telegram username=ivan_tg wrote", "ivan_tg", "wrote"},
	}
	for _, c := range cases {
		got := RedactPeople(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: the person is still there: %q", c.name, got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("%s: too much is gone, %q is missing: %q", c.name, c.keep, got)
		}
	}
	for _, line := range []string{
		`time=2026-10-14T10:00:00.123Z level=INFO msg="panel started" listen=0.0.0.0:21355 version=0.5.1.1`,
		"msg=update unit=mikan-update-request.service path=/opt/mikan/data/panel/update/request",
		"node 4 health ok users=120 inbounds=3 description=fine",
	} {
		if got := RedactPeople(line); got != line {
			t.Errorf("changed an ordinary line:\n%s\n%s", line, got)
		}
	}
}

func TestRedactWords(t *testing.T) {
	got := RedactWords("GET /secretpath123/api/v1/x /ab/y", []string{"secretpath123", "", "ab"})
	if strings.Contains(got, "secretpath123") || !strings.Contains(got, "/ab/y") {
		t.Fatalf("%q", got)
	}
}

func newLogger(b *Buffer, next slog.Handler) *slog.Logger { return slog.New(b.Handler(next)) }

// The buffer keeps the last lines in order, drops the oldest, and holds redacted text only.
func TestBufferKeepsTheLastLinesRedacted(t *testing.T) {
	b := New(3)
	log := newLogger(b, nil)
	for i := range 5 {
		log.Info("line", "n", i)
	}
	got := b.Tail(10)
	if len(got) != 3 {
		t.Fatalf("kept %d lines, want 3", len(got))
	}
	for i, e := range got {
		if !strings.Contains(e.Text, fmt.Sprintf("n=%d", i+2)) || e.Level != "INFO" || e.At.IsZero() {
			t.Errorf("line %d: %+v", i, e)
		}
	}
	if two := b.Tail(2); len(two) != 2 || !strings.Contains(two[0].Text, "n=3") || !strings.Contains(two[1].Text, "n=4") {
		t.Errorf("tail 2: %+v", two)
	}
	if b.Len() != 3 {
		t.Errorf("len %d", b.Len())
	}

	b = New(10)
	log = newLogger(b, nil)
	log.Warn("login failed", "password", "hunter2hunter2", "token", "ms_AbCdEfGhIjKlMnOpQrStUvWx", "ip", "203.0.113.5")
	log.With("authorization", "Bearer abcdefghijklmnop").Info("with attrs")
	for _, e := range b.Tail(0) {
		for _, s := range []string{"hunter2hunter2", "AbCdEfGhIjKl", "abcdefghijklmnop"} {
			if strings.Contains(e.Text, s) {
				t.Errorf("the buffer holds a secret %q: %s", s, e.Text)
			}
		}
	}
	if e := b.Tail(2); !strings.Contains(e[0].Text, "203.0.113.5") || e[0].Level != "WARN" {
		t.Errorf("the rest of the line is gone: %+v", e[0])
	}
}

// The handler in front of the real one changes nothing for it: it gets every record it takes,
// secrets included (the redacted copy is for the page only), and Debug stays off the buffer
// unless the real handler wants it.
func TestHandlerPassesRecordsOn(t *testing.T) {
	var out bytes.Buffer
	b := New(10)
	log := newLogger(b, slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelWarn}))
	log.Info("quiet", "token", "keepme-1234")
	log.Warn("loud", "token", "keepme-1234")
	if strings.Contains(out.String(), "quiet") || !strings.Contains(out.String(), "keepme-1234") {
		t.Errorf("the real handler's output changed: %q", out.String())
	}
	if n := b.Len(); n != 2 {
		t.Errorf("the buffer has %d lines, want both (Info is kept even when the real handler skips it)", n)
	}

	b = New(10)
	log = newLogger(b, slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Debug("noise")
	if b.Len() != 0 {
		t.Error("Debug reached the buffer")
	}
	b = New(10)
	log = newLogger(b, slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log.Debug("wanted")
	if b.Len() != 1 {
		t.Error("Debug that the real handler takes was not kept")
	}
}

// Many goroutines log at once: lines stay whole and the ring does not break (run with -race).
func TestConcurrentLogging(t *testing.T) {
	b := New(50)
	log := newLogger(b, nil)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 100 {
				log.With("g", g).Info("hello", "i", i, "password", "p4ssw0rd-p4ssw0rd")
			}
		})
	}
	wg.Wait()
	got := b.Tail(0)
	if len(got) != 50 {
		t.Fatalf("kept %d lines", len(got))
	}
	for _, e := range got {
		if !strings.Contains(e.Text, "msg=hello") || strings.Contains(e.Text, "p4ssw0rd") || strings.Count(e.Text, "\n") != 0 {
			t.Fatalf("a broken or unredacted line: %q", e.Text)
		}
	}
}
