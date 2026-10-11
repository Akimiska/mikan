package subs

import (
	"encoding/base64"
	"encoding/json"
)

// incyRoutingProfile is the routing profile the incy client (iOS/Xray) reads from a
// subscription line "://routing/onadd/{base64}" or the "routing" HTTP header. Its format
// is incy's own flat JSON (docs.incy.cc/subscription-format + /routing), NOT Xray's
// rules/outbound array: traffic is split into three fixed buckets by which *Sites/*Ip
// array a matcher lands in. Geo-categories (geosite:ru, geoip:ru), bare domains and
// IP/CIDR are accepted. This is how iOS clients that cannot set routing by hand still get
// the ".ru → direct" split, so Russian services see the user's real IP instead of the
// node's (VPN "detected" otherwise).
type incyRoutingProfile struct {
	Name           string   `json:"Name"`
	DirectSites    []string `json:"DirectSites"`
	DirectIP       []string `json:"DirectIp"`
	ProxySites     []string `json:"ProxySites"`
	ProxyIP        []string `json:"ProxyIp"`
	BlockSites     []string `json:"BlockSites"`
	BlockIP        []string `json:"BlockIp"`
	GlobalProxy    bool     `json:"GlobalProxy"`
	DomainStrategy string   `json:"DomainStrategy"`
	LastUpdated    int64    `json:"LastUpdated"`
}

// incyRoutingLine builds the "://routing/onadd/{base64}" subscription line carrying the
// RU-direct split for an incy-family client. Returns "" when routing must stay global
// (RoutingAll) — then everything goes through the tunnel and no profile is sent.
// lastUpdated gates whether incy replaces a stored profile with the same Name, so it is
// a fixed epoch bumped only when the rule set itself changes, not per request (otherwise
// every refresh would look newer and churn the client's profile).
func incyRoutingLine(r Routing, lastUpdated int64) string {
	if r != RoutingRUDirect {
		return ""
	}
	prof := incyRoutingProfile{
		Name: "AoiVPN",
		// Russian sites and IPs go direct: they must see the real RU IP, and routing them
		// through a foreign exit is what makes services flag the VPN.
		DirectSites: []string{"geosite:ru", "geosite:category-ru", "geosite:yandex", "geosite:category-gov-ru"},
		DirectIP:    []string{"geoip:ru", "geoip:private"},
		// Everything else proxied by default; no explicit proxy matchers needed.
		ProxySites:  []string{},
		ProxyIP:     []string{},
		BlockSites:  []string{},
		BlockIP:     []string{},
		GlobalProxy: true, // default (no match) → tunnel, like the clash MATCH,<main>
		// IPIfNonMatch: match by domain first, resolve to IP only when no domain rule hit —
		// same intent as the clash profile's resolver behaviour.
		DomainStrategy: "IPIfNonMatch",
		LastUpdated:    lastUpdated,
	}
	b, err := json.Marshal(prof)
	if err != nil {
		return ""
	}
	return "://routing/onadd/" + base64.StdEncoding.EncodeToString(b)
}

// incyRoutingHeader is the same RU-direct profile as a value for incy's "routing" HTTP
// header, which overrides the body. Raw base64 (not the "://routing/onadd/" line form).
func incyRoutingHeader(r Routing, lastUpdated int64) string {
	line := incyRoutingLine(r, lastUpdated)
	if line == "" {
		return ""
	}
	const prefix = "://routing/onadd/"
	return line[len(prefix):]
}

// incyRoutingEpoch is bumped by hand whenever incyRoutingLine's rule set changes, so the
// client replaces its stored "AoiVPN" profile. 2026-10-11: first version.
const incyRoutingEpoch = 1760140800
