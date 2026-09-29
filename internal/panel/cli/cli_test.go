package cli

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// The host script opens the new inbound's port in ufw from stdout, so it must be bare.
func TestInboundAddPrintsPortForHostScript(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := domain.Seed(ctx, st, time.Now()); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	var out, errOut bytes.Buffer
	if err := inboundCmd(ctx, st, set, []string{"add", "anytls"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "2083/tcp\n" {
		t.Fatalf("stdout must be port/network, got %q", out.String())
	}
	if err := inboundCmd(ctx, st, set, []string{"add", "anytls"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "anytls") {
		t.Fatalf("a taken port must name the owner: %v", err)
	}
	out.Reset()
	if err := inboundCmd(ctx, st, set, []string{"list"}, &out, &errOut); err != nil || !strings.Contains(out.String(), "2083/tcp") {
		t.Fatalf("list: %v %q", err, out.String())
	}
}

// A moved inbound's port goes to stdout for ufw on this server only; a remote node's port
// is opened on that node's server, so the panel's host script must not open it here.
func TestInboundSetPrintsPortOnlyForOwnNode(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	if err := domain.Seed(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	set := settings.New(st.Q)
	var out, errOut bytes.Buffer
	if err := inboundCmd(ctx, st, set, []string{"set", "vless-xhttp", "--port", "2443"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.String() != "2443/tcp\n" || !strings.Contains(errOut.String(), "443 → 2443/tcp") {
		t.Fatalf("own node: stdout %q, stderr %q", out.String(), errOut.String())
	}

	n, err := st.Q.CreateNode(ctx, db.CreateNodeParams{Name: "🇺🇸 США", Address: "203.0.113.7:25305", PublicHost: "203.0.113.7", CreatedAt: now.Unix(), UpdatedAt: now.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	node := strconv.FormatInt(n.ID, 10)
	for _, args := range [][]string{
		{"add", "hysteria2", "--node", node, "--port", "2443"},
		{"set", "hysteria2", "--node", node, "--port", "3443"},
	} {
		out.Reset()
		errOut.Reset()
		if err := inboundCmd(ctx, st, set, args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out.Len() != 0 || !strings.Contains(errOut.String(), "ufw allow "+args[len(args)-1]+"/udp") {
			t.Fatalf("%v: remote node must not print a rule for this server: stdout %q, stderr %q", args, out.String(), errOut.String())
		}
	}
	if err := inboundCmd(ctx, st, set, []string{"set", "nope", "--port", "3000"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("unknown inbound: %v", err)
	}
}

// The host `mikan` script of every installed version waits for the panel after an update
// with path=$(admin url | sed -E 's#https?://[^/]+/##'); 0.1.2 broke it by printing
// "Адрес: ..." and the login on stdout, and every update rolled back.
func TestURLStdoutParsesInHostScript(t *testing.T) {
	var out, errOut bytes.Buffer
	printURL(&out, &errOut, "https://vpn.example.com:21355/Abc123secret/", "k7x2m9qfa4tw")
	if out.String() != "https://vpn.example.com:21355/Abc123secret/\n" {
		t.Fatalf("stdout must be the bare link, got %q", out.String())
	}
	path := regexp.MustCompile(`https?://[^/]+/`).ReplaceAllString(strings.TrimSpace(out.String()), "")
	if path != "Abc123secret/" {
		t.Fatalf("host script would request %q", path)
	}
	if !strings.Contains(errOut.String(), "k7x2m9qfa4tw") {
		t.Fatalf("the login still shows in the terminal: %q", errOut.String())
	}
}
