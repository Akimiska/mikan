package domain

import "strings"

// aoiApp (AoiVPN fork) is the app a known device keeps: the AoiVPN app also fetches the
// subscription as "sing-box/1.14.2" in the background (its naive core, for the legacy
// links), with the same device id; that fetch must not rename the device from AoiVPN to
// sing-box. A device first seen by such a fetch takes it as usual.
func aoiApp(app, old string) string {
	if strings.HasPrefix(strings.ToLower(app), "sing-box/") && old != "" && !strings.HasPrefix(strings.ToLower(old), "sing-box") {
		return old
	}
	return app
}
