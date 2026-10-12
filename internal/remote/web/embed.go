// Package web embeds the phone client built from web/remote. The built files
// are committed so `go install` needs no JavaScript toolchain.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built client rooted at its index.html.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at build time; Sub only fails on a bad name
	}
	return sub
}
