package ui

import (
	"embed"
	"io/fs"
)

// static holds htmx, its server-sent events extension and the stylesheet.
//
//go:embed static/*.js static/*.css static/LICENSE.*
var static embed.FS

// Static returns the files served under /static/: htmx, its server-sent
// events extension, their licenses and the stylesheet.
func Static() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		// The directory is embedded at build time, so Sub cannot fail.
		panic(err)
	}

	return sub
}
