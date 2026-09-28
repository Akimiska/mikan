package web

import (
	"embed"
	"io/fs"
)

// dist is produced by `pnpm build` in this directory and must exist before `go build ./cmd/mikan`.
//
//go:embed all:dist
var dist embed.FS

func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
