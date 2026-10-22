package subs

import "strings"

// ClientRules (AoiVPN fork) says whether the User-Agent is a VPN app at all (client) and
// whether that app gets the subscription's routing rules — the torrents rule among them that
// sends torrent traffic past the nodes, DIRECT (rules). mihomo and Stash apps take the rules
// with the profile, and so does the official sing-box app that runs the whole config; xray
// apps (v2rayNG, Happ, INCY…) and the rest get links only and route by their own settings.
//
// Not a client: a browser opening the subscription page, curl, link-preview bots, and an
// app field with no version at all. The bare "sing-box/1.x" is AoiVPN before 0.3.1: its
// naive helper fetched the profile, the traffic still went through mihomo and its rules.
func ClientRules(userAgent string) (client, rules bool) {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	for _, notApp := range []string{"mozilla/", "curl/", "wget/", "vkshare", "telegrambot", "python-", "go-http-client"} {
		if strings.Contains(ua, notApp) {
			return false, false
		}
	}
	if strings.HasPrefix(ua, "sing-box/") {
		return true, true
	}
	app := DetectApp(userAgent)
	switch app.Family {
	case FamilyMihomo, FamilyStash:
		return true, true
	case FamilySingBox:
		return true, app.Whole
	case FamilyXray:
		return true, false
	}
	// An app we do not know (Shadowrocket, Streisand…) names itself with a version.
	return strings.Contains(ua, "/"), false
}
