// Package web holds the browser assets that ship inside the veo binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// Static returns the comparison player's files, rooted at index.html.
func Static() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic("web: static assets missing from the binary: " + err.Error())
	}
	return sub
}
