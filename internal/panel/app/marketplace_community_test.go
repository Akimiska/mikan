package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"mikan/internal/panel/addons"
)

// communityAddon is a snapshot of the registry: an entry of an author's release, its repository
// and the author's key ("" unsigned).
func communityAddon(id, version, digestChar, key string, perms ...string) map[string]any {
	a := srcEntry(id, version, digestChar, "generic", perms...)
	a["repo"], a["repo_id"], a["stars"], a["downloads"], a["added_at"], a["updated_at"] = "github.com/Author/"+id, 7, 3, 1300, 100, 200
	if key != "" {
		a["key"] = key
	}
	return a
}

// publishRegistry sets the registry's index; the panel reads it again at once.
func (l *srcLab) publishRegistry(snapshots []map[string]any, revoked ...map[string]any) {
	l.t.Helper()
	if revoked == nil {
		revoked = []map[string]any{}
	}
	data, err := json.Marshal(map[string]any{"version": 1, "updated": "2026-10-14T00:00:00Z", "addons": snapshots, "revoked": revoked})
	if err != nil {
		l.t.Fatal(err)
	}
	l.mu.Lock()
	l.registry = data
	l.mu.Unlock()
	l.h.p.Addons.SetCommunityURL(l.registryURL)
}

type communityV struct {
	Community []struct {
		ID               string `json:"id"`
		Installed        bool   `json:"installed"`
		Update           bool   `json:"update"`
		InstalledDigest  string `json:"installed_digest"`
		InstalledVersion string `json:"installed_version"`
		KeyChanged       bool   `json:"key_changed"`
		Fingerprint      string `json:"fingerprint"`
		Stars            int    `json:"stars"`
		Downloads        int    `json:"downloads"`
		Origin           struct {
			Kind    string `json:"kind"`
			Repo    string `json:"repo"`
			Signed  bool   `json:"signed"`
			Revoked string `json:"revoked"`
		} `json:"origin"`
	} `json:"community"`
	CommunityError string `json:"community_error"`
	Installed      []struct {
		ID     string `json:"id"`
		Origin *struct {
			Kind    string `json:"kind"`
			Revoked string `json:"revoked"`
		} `json:"origin"`
	} `json:"installed"`
}

func (l *srcLab) communityView() communityV {
	l.t.Helper()
	var v communityV
	if err := json.Unmarshal(l.call(http.MethodGet, "/marketplace", nil, http.StatusOK), &v); err != nil {
		l.t.Fatal(err)
	}
	return v
}

func (l *srcLab) communityRow(id string) (string, bool) {
	l.t.Helper()
	rows, err := l.h.st.Q.ListAddonCommunity(context.Background())
	if err != nil {
		l.t.Fatal(err)
	}
	for _, r := range rows {
		if r.AddonID == id {
			return r.Key, true
		}
	}
	return "", false
}

func TestCommunityAddons(t *testing.T) {
	l := newSrcLab(t)
	if v := l.communityView(); v.CommunityError != "community_unavailable" || len(v.Community) != 0 {
		t.Fatalf("no registry: %+v", v)
	}
	k1, k2 := newKey(t), newKey(t)
	key1 := base64.StdEncoding.EncodeToString(k1.Public().(ed25519.PublicKey))
	key2 := base64.StdEncoding.EncodeToString(k2.Public().(ed25519.PublicKey))
	// An id of the catalog is never the registry's, and a revoked addon is not offered.
	l.publishRegistry([]map[string]any{communityAddon("hello", "1.0.0", "b", key1, "kv"), communityAddon("yookassa", "1.0.0", "b", ""),
		communityAddon("evil", "1.0.0", "e", "")}, map[string]any{"id": "evil", "reason": "malware", "at": "2026-10-14"})
	v := l.communityView()
	if v.CommunityError != "" || len(v.Community) != 1 {
		t.Fatalf("community: %+v", v)
	}
	if c := v.Community[0]; c.ID != "hello" || c.Origin.Kind != "community" || c.Origin.Repo != "https://github.com/Author/hello" || !c.Origin.Signed ||
		c.Fingerprint != addons.Fingerprint(key1) || c.Stars != 3 || c.Downloads != 1300 || c.Installed {
		t.Fatalf("entry: %+v", c)
	}

	// The admin approves the permissions and says they take the risk; nothing goes without both.
	l.call(http.MethodPost, "/marketplace/hello/install", map[string]any{"permissions": []string{"kv"}}, http.StatusUnprocessableEntity, "addon_not_confirmed")
	l.call(http.MethodPost, "/marketplace/hello/install", approve(), http.StatusConflict, "permissions_changed")
	l.call(http.MethodPost, "/marketplace/evil/install", approve(), http.StatusConflict, "addon_revoked")
	if _, ok := l.grant("hello"); ok {
		t.Fatal("a refused install leaves no grant")
	}
	l.call(http.MethodPost, "/marketplace/hello/install", approve("kv"), http.StatusAccepted)
	if key, ok := l.communityRow("hello"); !ok || key != key1 {
		t.Fatalf("the registry's row: %q %v", key, ok)
	}
	if _, ok := l.origin("hello"); ok {
		t.Fatal("no source's row")
	}
	reqs := l.h.p.Addons.Pending()
	if len(reqs) != 1 || reqs[0].Source != addons.SourceCustom || reqs[0].Image != "registry.example.com/me/hello" || reqs[0].Digest != digestOf("b") {
		t.Fatalf("the request: %+v", reqs)
	}
	l.hostRan(map[string]addons.Installed{"hello": {Image: "registry.example.com/me/hello", Digest: digestOf("b"), Status: "running", Source: addons.SourceCustom, Kind: addons.KindGeneric, Protocol: 2}})
	v = l.communityView()
	if c := v.Community[0]; !c.Installed || c.Update || len(v.Installed) != 1 || v.Installed[0].Origin == nil || v.Installed[0].Origin.Kind != "community" {
		t.Fatalf("installed: %+v", v)
	}
	l.call(http.MethodPost, "/marketplace/hello/install", approve("kv"), http.StatusConflict, "addon_current")

	// Another addon may not take the registry's id: a process of the admin's own, a source.
	l.call(http.MethodPost, "/marketplace/external", map[string]any{"id": "hello", "kind": "generic", "mode": "client", "permissions": []string{}, "confirm": true},
		http.StatusUnprocessableEntity, "addon_id_taken")
	l.publish("/a.json", l.catalogDoc(srcEntry("hello", "9.0.0", "f", "generic")), nil)
	l.call(http.MethodPost, "/addon-sources", map[string]any{"url": l.url("/a.json")}, http.StatusUnprocessableEntity, "addon_id_taken")

	// A new build: an update, with what runs now shown beside it, and the risk confirmed again.
	l.publishRegistry([]map[string]any{communityAddon("hello", "1.1.0", "c", key1, "kv")})
	v = l.communityView()
	if c := v.Community[0]; !c.Update || c.InstalledDigest != digestOf("b") || c.InstalledVersion != "1.0.0" || c.KeyChanged {
		t.Fatalf("update: %+v", c)
	}
	l.call(http.MethodPost, "/marketplace/hello/install", map[string]any{"permissions": []string{"kv"}}, http.StatusUnprocessableEntity, "addon_not_confirmed")

	// Signed by another key: the admin compares it and takes it, or nothing goes.
	l.publishRegistry([]map[string]any{communityAddon("hello", "1.2.0", "d", key2, "kv")})
	if c := l.communityView().Community[0]; !c.KeyChanged || c.Fingerprint != addons.Fingerprint(key2) {
		t.Fatalf("key changed: %+v", c)
	}
	l.call(http.MethodPost, "/marketplace/hello/install", approve("kv"), http.StatusConflict, "author_key_changed")
	wrong := approve("kv")
	wrong["accept_key"] = addons.Fingerprint(key1)
	l.call(http.MethodPost, "/marketplace/hello/install", wrong, http.StatusConflict, "author_key_changed")
	take := approve("kv")
	take["accept_key"] = addons.Fingerprint(key2)
	l.call(http.MethodPost, "/marketplace/hello/install", take, http.StatusAccepted)
	if key, _ := l.communityRow("hello"); key != key2 {
		t.Fatalf("the key taken: %q", key)
	}
	l.hostRan(map[string]addons.Installed{"hello": {Image: "registry.example.com/me/hello", Digest: digestOf("d"), Status: "running", Source: addons.SourceCustom, Kind: addons.KindGeneric, Protocol: 2}})

	// No longer signed at all: never taken, whatever the admin sends.
	l.publishRegistry([]map[string]any{communityAddon("hello", "1.3.0", "e", "", "kv")})
	unsigned := approve("kv")
	unsigned["accept_key"] = addons.Fingerprint(key2)
	l.call(http.MethodPost, "/marketplace/hello/install", unsigned, http.StatusConflict, "author_key_changed")

	// Revoked: the installed addon says why, and nothing of it installs again.
	l.publishRegistry([]map[string]any{}, map[string]any{"repo": "github.com/author/hello", "reason": "stolen code", "at": "2026-10-14"})
	v = l.communityView()
	if len(v.Community) != 0 || v.Installed[0].Origin == nil || v.Installed[0].Origin.Revoked != "stolen code" {
		t.Fatalf("revoked: %+v", v)
	}
	l.call(http.MethodPost, "/marketplace/hello/install", approve("kv"), http.StatusConflict, "addon_revoked")

	// Removed: the row goes with the grant.
	l.call(http.MethodPost, "/marketplace/hello/remove", nil, http.StatusAccepted)
	if _, ok := l.communityRow("hello"); ok {
		t.Fatal("the registry's row outlives the addon")
	}
}

// A source whose repository mikan revoked installs nothing either.
func TestCommunityRevokesASource(t *testing.T) {
	l := newSrcLab(t)
	l.publish("/a.json", l.catalogDoc(srcEntry("alpha", "1.0.0", "b", "generic")), nil)
	l.call(http.MethodPost, "/addon-sources", map[string]any{"url": l.url("/a.json")}, http.StatusCreated)
	l.publishRegistry([]map[string]any{}, map[string]any{"repo": "github.com/x/y", "reason": "r", "at": "2026-10-14"})
	l.call(http.MethodPost, "/marketplace/alpha/install", approve(), http.StatusAccepted)
	// The source's address is the revoked repository's.
	if _, err := l.h.st.DB.ExecContext(context.Background(), "UPDATE addon_sources SET url = 'github.com/X/Y'"); err != nil {
		t.Fatal(err)
	}
	l.hostRan(map[string]addons.Installed{})
	l.call(http.MethodPost, "/marketplace/alpha/install", approve(), http.StatusConflict, "addon_revoked")
}
