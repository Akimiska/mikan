package addons

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/release"
)

func communityDoc(addons []map[string]any, revoked []map[string]any) []byte {
	data, _ := json.Marshal(map[string]any{"version": 1, "updated": "2026-10-14T00:00:00Z", "addons": addons, "revoked": revoked})
	return data
}

func communityAddon(id, repo, digestChar string) map[string]any {
	a := entryDoc(id, digestChar)
	a["repo"], a["repo_id"], a["stars"], a["added_at"], a["updated_at"] = repo, 7, 2, 100, 100
	return a
}

func TestParseCommunity(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	authorPub, _, _ := ed25519.GenerateKey(rand.Reader)
	signedA := communityAddon("signed", "github.com/Author/signed", "b")
	signedA["key"] = base64.StdEncoding.EncodeToString(authorPub)
	signedA["previous"] = map[string]any{"version": "0.9.0", "digest": "sha256:" + strings.Repeat("a", 64)}
	pay := communityAddon("pay", "github.com/Author/pay", "c")
	pay["protocol"], pay["kind"] = 1, "payment"
	tagged := communityAddon("tagged", "github.com/Author/tagged", "c")
	tagged["digest"] = "latest"
	lan := communityAddon("lan", "github.com/Author/lan", "c")
	lan["image"] = "10.0.0.1/me/lan"
	badRepo := communityAddon("badrepo", "gitlab.com/Author/x", "c")
	badKey := communityAddon("badkey", "github.com/Author/badkey", "c")
	badKey["key"] = "nope"
	newer := communityAddon("newer", "github.com/Author/newer", "c")
	newer["min_panel"] = "9.9.9"
	tg := communityAddon("telegram", "github.com/Author/telegram", "c")
	evil := communityAddon("evil", "github.com/Bad/evil", "d")
	alsoBad := communityAddon("alsobad", "github.com/bad/EVIL", "d")
	data := communityDoc([]map[string]any{communityAddon("hello", "github.com/Author/hello", "b"), communityAddon("hello", "github.com/Other/hello", "c"),
		signedA, pay, tagged, lan, badRepo, badKey, newer, tg, evil, alsoBad},
		[]map[string]any{{"repo": "github.com/Bad/evil", "reason": "stolen code\n<b>", "at": "2026-10-14"}, {"id": "Bad Id", "reason": "x", "at": "2026-10-14"}})
	sig := release.SignIn(release.CommunityContext, data, priv)
	c, err := ParseCommunity(data, sig, pub, "0.5.1.0")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range c.Addons {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "hello,signed" {
		t.Fatalf("addons %v", ids)
	}
	if c.Addons[0].Repo != "github.com/Author/hello" || c.Addons[0].Mode != ModeHTTP {
		t.Fatalf("the first listing of an id stays, cleaned: %+v", c.Addons[0])
	}
	if c.Addons[1].Previous == nil || c.Addons[1].Key == "" {
		t.Fatalf("signed: %+v", c.Addons[1])
	}
	if len(c.Revoked) != 1 || c.Revoked[0].Reason != "stolen code <b>" {
		t.Fatalf("revoked %+v", c.Revoked)
	}
	if r, out := c.RevokedFor("", "github.com/bad/evil"); !out || r.At != "2026-10-14" {
		t.Fatal("revoked by repository, whatever the case")
	}
	if _, err := ParseCommunity(append(data, ' '), sig, pub, "0.5.1.0"); err == nil {
		t.Fatal("a tampered index")
	}
}

func TestManagerCommunityCaches(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	data := communityDoc([]map[string]any{communityAddon("hello", "github.com/Author/hello", "b")}, nil)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(data)
		case "/index.json" + release.SigSuffix:
			_, _ = io.WriteString(w, release.SignIn(release.CommunityContext, data, priv))
		}
	}))
	defer srv.Close()
	now := time.Unix(1000, 0)
	m := New("", "", "dev", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	m.SetKeys(pub, pub)
	if m.CommunityKnown() != nil {
		t.Fatal("nothing known before the first fetch")
	}
	m.SetCommunityURL(srv.URL + "/index.json")
	for range 2 {
		if c, err := m.Community(context.Background()); err != nil || len(c.Addons) != 1 {
			t.Fatalf("community: %v", err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("fetched again within ten minutes: %d", hits.Load())
	}
	now = now.Add(11 * time.Minute)
	if _, err := m.Community(context.Background()); err != nil || hits.Load() != 4 {
		t.Fatalf("stale: %v %d", err, hits.Load())
	}
	if k := m.CommunityKnown(); k == nil || len(k.Addons) != 1 {
		t.Fatal("known")
	}
}
