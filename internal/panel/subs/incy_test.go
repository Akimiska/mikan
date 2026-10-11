package subs

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestIncyRoutingLine(t *testing.T) {
	// RoutingAll → no routing profile (everything through the tunnel).
	if got := incyRoutingLine(RoutingAll, incyRoutingEpoch); got != "" {
		t.Fatalf("RoutingAll must send no routing profile, got %q", got)
	}

	line := incyRoutingLine(RoutingRUDirect, incyRoutingEpoch)
	if !strings.HasPrefix(line, "://routing/onadd/") {
		t.Fatalf("line prefix: %q", line)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "://routing/onadd/"))
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	var p incyRoutingProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("json: %v", err)
	}
	// Russian domains go direct by plain-domain match (NO geosite: — incy's geosite.dat
	// has no RU code and one unknown tag rejects the whole Xray routing config).
	hasRU := false
	hasGeosite := false
	for _, s := range p.DirectSites {
		if s == "ru" || s == "yandex.net" || s == "vk.com" {
			hasRU = true
		}
		if strings.HasPrefix(s, "geosite:") {
			hasGeosite = true
		}
	}
	if !hasRU {
		t.Errorf("DirectSites must carry plain RU domains: %v", p.DirectSites)
	}
	if hasGeosite {
		t.Errorf("DirectSites must NOT use geosite: (breaks incy Xray config): %v", p.DirectSites)
	}
	hasRUIP := false
	for _, s := range p.DirectIP {
		if s == "geoip:ru" {
			hasRUIP = true
		}
	}
	if !hasRUIP {
		t.Errorf("DirectIp must carry geoip:ru: %v", p.DirectIP)
	}
	if !p.GlobalProxy {
		t.Errorf("GlobalProxy must be true (default → tunnel)")
	}
	if p.DomainStrategy != "IPIfNonMatch" {
		t.Errorf("DomainStrategy: %q", p.DomainStrategy)
	}
	if p.LastUpdated != incyRoutingEpoch {
		t.Errorf("LastUpdated must be the fixed epoch, got %d", p.LastUpdated)
	}

	// The header form is the same payload, raw base64 without the scheme prefix.
	hdr := incyRoutingHeader(RoutingRUDirect, incyRoutingEpoch)
	if hdr != strings.TrimPrefix(line, "://routing/onadd/") {
		t.Errorf("header must be the line's base64 payload")
	}
	if incyRoutingHeader(RoutingAll, incyRoutingEpoch) != "" {
		t.Errorf("RoutingAll header must be empty")
	}
}
