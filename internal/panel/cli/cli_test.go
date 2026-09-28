package cli

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
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
