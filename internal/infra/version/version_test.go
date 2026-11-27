package version_test

import (
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/version"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// devel is what versioninfo says without build information, and stamped
// what a release stamps.
const (
	devel   = "devel"
	stamped = "v0.1.0"
)

// The stamped version and commit win over what Go recorded, which in the
// image is only devel.
func TestResolve(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, version, commit, fallback, want string
	}{
		{"both stamped", stamped, "0123456789abcdef", devel, "v0.1.0 rev 0123456"},
		{"only the version", stamped, "", "v0.0.9 rev fedcba9", stamped},
		{"only the commit", "", "0123abc", devel, "rev 0123abc"},
		{"a short commit", "v1.2.3", "abc", devel, "v1.2.3 rev abc"},
		{"nothing stamped", "", "", "v0.0.9 rev fedcba9 dirty", "v0.0.9 rev fedcba9 dirty"},
		{"blank build arguments", " ", "\n", devel, devel},
	} {
		testutil.Equal(t, tc.name, version.Resolve(tc.version, tc.commit, tc.fallback), tc.want)
	}
}

// A plain go test build stamps nothing, so Short is never empty.
func TestShort(t *testing.T) {
	t.Parallel()

	testutil.True(t, "a version", version.Short() != "")
}
