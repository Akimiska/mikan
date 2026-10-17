package subs

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Happ is what the panel tells Happ beyond the profile (Settings → Subscription → Happ),
// per its provider docs (happ.su/main/dev-docs).
type Happ struct {
	// Routing is a happ://routing/... link: Happ adds the routing profile with the
	// subscription ("onadd" also turns it on). Sent to Happ only. HappRoutingAuto: the
	// panel makes it of the Clash profile's rules (happroute.go).
	Routing string
	// ProviderID ties the subscription to the admin's account at happ-proxy.com. Happ takes
	// the provider headers (HideSettings) only with it.
	ProviderID   string
	HideSettings bool
	// Crypt is how the subscription page's Happ button hides the subscription address:
	// "" plain, HappCryptAPI by Happ's own service, HappCryptLocal by the panel itself.
	Crypt string
}

const (
	HappCryptAPI   = "api"
	HappCryptLocal = "local"
)

// HappRoutingMax bounds the routing link: a profile with geo lists stays far below it.
const HappRoutingMax = 64 << 10

var happProvider = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidHappProviderID: the ids happ-proxy.com gives out are short words of letters, digits,
// "-" and "_"; anything else would not survive a header.
func ValidHappProviderID(s string) bool { return happProvider.MatchString(s) }

// ValidHappRouting accepts happ://routing/off, HappRoutingAuto, and the add/onadd links
// whose payload is the base64 of a JSON profile: what Happ itself exports.
func ValidHappRouting(s string) bool {
	if s == "happ://routing/off" || s == HappRoutingAuto {
		return true
	}
	if len(s) > HappRoutingMax || strings.ContainsAny(s, " \r\n\t") {
		return false
	}
	var payload string
	for _, p := range []string{"happ://routing/onadd/", "happ://routing/add/"} {
		if rest, ok := strings.CutPrefix(s, p); ok {
			payload = rest
		}
	}
	if payload == "" {
		return false
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(payload); err == nil {
			return json.Valid(raw)
		}
	}
	return false
}

// IsHapp tells Happ by its User-Agent ("Happ/3.6.0/…").
func IsHapp(ua string) bool { return strings.Contains(strings.ToLower(ua), "happ") }

// happHeaders are the provider headers Happ reads; other apps never get them.
func happHeaders(h http.Header, c Happ) {
	if c.Routing != "" {
		h.Set("routing", c.Routing)
	}
	if c.ProviderID == "" {
		return
	}
	h.Set("providerid", c.ProviderID)
	if c.HideSettings {
		h.Set("hide-settings", "1")
	}
}
