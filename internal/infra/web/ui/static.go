package ui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sync"
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

// stylesheetVersionLength is how many hex digits of the stylesheet's hash
// its address carries, as the brand's files do.
const stylesheetVersionLength = 16

// StylesheetURL is the address of app.css with a hash of its content in the
// query, so a browser that kept an older stylesheet fetches the new one as
// soon as a new wpfleet serves pages that need it.
var StylesheetURL = sync.OnceValue(func() string { //nolint:gochecknoglobals // hashed once from the embedded file
	data, err := static.ReadFile("static/app.css")
	if err != nil {
		// The file is embedded at build time, so reading it cannot fail.
		panic(err)
	}

	sum := sha256.Sum256(data)

	return "/static/app.css?v=" + hex.EncodeToString(sum[:])[:stylesheetVersionLength]
})
