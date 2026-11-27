// Package version says which wpfleet this is. A release build stamps the
// version and the commit with the linker:
//
//	go build -ldflags "-X github.com/rahacloud/wpfleet/internal/infra/version.version=v0.1.0 \
//	  -X github.com/rahacloud/wpfleet/internal/infra/version.commit=0123abc" ./cmd/wpfleet
//
// Without them it falls back to what Go recorded from the module and the
// version control system, through versioninfo. The image is built from a
// context without .git, where that fallback only says "devel", and
// versioninfo sets its own variables from the build information when the
// program starts, so a -X on them would be overwritten; the variables here
// are wpfleet's own and nothing overwrites them.
package version

import (
	"strings"

	"github.com/carlmjohnson/versioninfo"
)

// Set by the linker; empty in a plain go build.
var (
	version = ""
	commit  = "" //nolint:gochecknoglobals // the linker sets it with -X
)

// shortCommit is how many hex digits of the commit the version shows, as
// versioninfo shows them.
const shortCommit = 7

// Short is the version as --version prints it, e.g. "v0.1.0 rev 0123abc".
func Short() string {
	return resolve(version, commit, versioninfo.Short())
}

// resolve prefers the stamped version and commit; with neither it is the
// fallback.
func resolve(version, commit, fallback string) string {
	version, commit = strings.TrimSpace(version), strings.TrimSpace(commit)

	if version == "" && commit == "" {
		return fallback
	}

	parts := make([]string, 0, 3) //nolint:mnd // version, "rev" and the commit

	if version != "" {
		parts = append(parts, version)
	}

	if commit != "" {
		parts = append(parts, "rev", commit[:min(len(commit), shortCommit)])
	}

	return strings.Join(parts, " ")
}
