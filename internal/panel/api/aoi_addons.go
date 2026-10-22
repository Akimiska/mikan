package api

import "mikan/internal/panel/addons"

// sameImage (AoiVPN fork): an installed addon is offered the catalog's build as an update only
// when it runs the catalog's image. The fork's own bot (ghcr.io/akimiska/mikan) always differs
// in digest from the catalog's tool-telegram, and "updating" it would replace the fork's bot
// with the upstream one.
func sameImage(a addons.Installed, e addons.Entry) bool {
	return a.Image == "" || e.Image == "" || a.Image == e.Image
}
