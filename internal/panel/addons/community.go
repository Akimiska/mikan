package addons

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mikan/internal/release"
)

// The registry of community addons (getmikan/marketplace-community): addons of third-party
// authors that mikan lists and does not vouch for. A mikan workflow reads the authors'
// releases and publishes a snapshot of each addon (its image by digest, its permissions) in
// one index.json signed with the catalog's key, so the panel reads one file from GitHub and
// never the authors' hosts. An addon from it installs as an unofficial one, as an addon of a
// source does; revoked lists what mikan took out, which a panel that has it installed shows.

// CommunityURL is where the registry's signed index is published, next to its .sig.
const CommunityURL = "https://github.com/getmikan/marketplace-community/releases/latest/download/index.json"

// CommunityPrior is the build of an addon the registry had before the current one.
type CommunityPrior struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// CommunityAddon is one snapshot: the entry as its author's release has it, where it comes
// from, and the author's key the release was signed with ("" unsigned).
type CommunityAddon struct {
	Entry
	Repo   string `json:"repo"`
	RepoID int64  `json:"repo_id"`
	Key    string `json:"key,omitempty"`
	Stars  int    `json:"stars"`
	// Downloads: the image's pulls as its GitHub package counts them, two significant digits;
	// 0 when the registry has none.
	Downloads int             `json:"downloads,omitempty"`
	Previous  *CommunityPrior `json:"previous,omitempty"`
	AddedAt   int64           `json:"added_at"`
	UpdatedAt int64           `json:"updated_at"`
}

// CommunityRevoked is an addon mikan took out of the registry, by id or by repository.
type CommunityRevoked struct {
	ID     string `json:"id,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// Community is the registry's index.
type Community struct {
	Version int                `json:"version"`
	Updated time.Time          `json:"updated"`
	Addons  []CommunityAddon   `json:"addons"`
	Revoked []CommunityRevoked `json:"revoked"`
}

// Revoked is the revocation of the addon id from the repository (either may be ""), if any.
func (c Community) RevokedFor(id, repo string) (CommunityRevoked, bool) {
	for _, r := range c.Revoked {
		if id != "" && r.ID == id || repo != "" && r.Repo != "" && strings.EqualFold(r.Repo, repo) {
			return r, true
		}
	}
	return CommunityRevoked{}, false
}

// Addon is the registry's snapshot of id.
func (c Community) Addon(id string) (CommunityAddon, bool) {
	for _, a := range c.Addons {
		if a.ID == id {
			return a, true
		}
	}
	return CommunityAddon{}, false
}

// validRepo is github.com/<owner>/<repo>, as the registry names a repository.
func validRepo(r string) bool {
	parts := strings.Split(r, "/")
	return len(parts) == 3 && parts[0] == "github.com" && ghOwner.MatchString(parts[1]) && ghName.MatchString(parts[2]) && parts[2] != "." && parts[2] != ".."
}

// ParseCommunity checks the signature over the exact bytes, then each snapshot as an entry of
// a third-party source: what this panel would not install from one (a payment adapter, a tag
// instead of a digest, a private registry, a newer panel) is left out, and so is an id listed
// twice or revoked. A revocation keeps a short reason; the page shows it.
func ParseCommunity(data []byte, sig string, pub ed25519.PublicKey, panelVersion string) (Community, error) {
	if err := release.Verify(data, sig, pub); err != nil {
		return Community{}, fmt.Errorf("addons: the community index signature does not match: %w", err)
	}
	var c Community
	if err := json.Unmarshal(data, &c); err != nil {
		return Community{}, fmt.Errorf("addons: community index: %w", err)
	}
	revoked := c.Revoked[:0]
	for _, r := range c.Revoked {
		r.Reason, r.At = cleanLine(r.Reason, 300), cleanLine(r.At, 10)
		if r.ID != "" && !ValidID(r.ID) || r.Repo != "" && !validRepo(r.Repo) || r.ID == "" && r.Repo == "" {
			continue
		}
		revoked = append(revoked, r)
	}
	c.Revoked = revoked
	seen := map[string]bool{}
	out := c.Addons[:0]
	for _, a := range c.Addons {
		if checkSourceEntry(&a.Entry, panelVersion) != "" || !validRepo(a.Repo) || a.Key != "" && !validKey(a.Key) || seen[a.ID] {
			continue
		}
		if _, gone := c.RevokedFor(a.ID, a.Repo); gone {
			continue
		}
		if a.Previous != nil && (!release.ValidDigest(a.Previous.Digest) || !validVersion.MatchString(a.Previous.Version)) {
			a.Previous = nil
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	c.Addons = out
	return c, nil
}

func validKey(k string) bool {
	_, ok := ParseSourceKey(k)
	return ok
}

// SetCommunityURL points the panel at another registry (tests).
func (m *Manager) SetCommunityURL(u string) {
	m.mu.Lock()
	m.communityURL, m.community = u, nil
	m.mu.Unlock()
}

// CommunityKnown is the index fetched last, however old; nil before the first fetch. What only
// judges ids (sources.go) takes it rather than reach the network.
func (m *Manager) CommunityKnown() *Community {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.community == nil {
		return nil
	}
	c := *m.community
	return &c
}

// Community is the registry's signed index, fetched at most every ten minutes.
func (m *Manager) Community(ctx context.Context) (Community, error) {
	m.mu.Lock()
	if m.community != nil && m.now().Sub(m.communityAt) < 10*time.Minute {
		c := *m.community
		m.mu.Unlock()
		return c, nil
	}
	u := m.communityURL
	m.mu.Unlock()
	var c Community
	err := release.FetchSigned(ctx, m.get, u, 1<<20, func(data []byte, sig string) (err error) {
		c, err = ParseCommunity(data, sig, m.pub, m.version)
		return err
	})
	if err != nil {
		return Community{}, err
	}
	m.mu.Lock()
	m.community, m.communityAt = &c, m.now()
	m.mu.Unlock()
	return c, nil
}
