package subs

// RuleCapable (AoiVPN fork) says whether the app gets the subscription's routing rules — the
// torrents rule among them that sends torrent traffic past the nodes, DIRECT. mihomo and
// Stash apps take the rules with the profile, and so does the official sing-box app that runs
// the whole config; xray apps (v2rayNG, Happ, v2RayTun…) and the rest get links only, and
// route by their own settings.
func RuleCapable(userAgent string) bool {
	app := DetectApp(userAgent)
	switch app.Family {
	case FamilyMihomo, FamilyStash:
		return true
	case FamilySingBox:
		return app.Whole
	}
	return false
}
