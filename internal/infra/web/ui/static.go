package ui

import (
	"embed"
	"io/fs"
)

// FaviconFile is wpfleet's own favicon in Static, used when a fleet sets
// none.
const FaviconFile = "favicon.svg"

// static holds htmx, its server-sent events extension, the stylesheet, the
// Persian font and wpfleet's favicon.
//
//go:embed static/*.js static/*.css static/*.woff2 static/*.svg static/LICENSE.*
var static embed.FS

// Static returns the files served under /static/: htmx, its server-sent
// events extension, the stylesheet, the Persian font, the favicon and their
// licenses.
func Static() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		// The directory is embedded at build time, so Sub cannot fail.
		panic(err)
	}

	return sub
}
