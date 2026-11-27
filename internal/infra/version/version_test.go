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
	// described is a short commit as git describe --always prints it.
	described = "8c38585"
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
		// git describe --always names the commit itself when there is no
		// tag, and after a tag it ends in g<commit>; the commit is not
		// repeated.
		{"describe without a tag", described, described, devel, described},
		{"describe without a tag, dirty", described + "-dirty", described, devel, described + "-dirty"},
		{"describe after a tag", stamped + "-3-g" + described, described, devel, stamped + "-3-g" + described},
		{"a longer commit", described, described + "d1e2f", devel, described},
	} {
		testutil.Equal(t, tc.name, version.Resolve(tc.version, tc.commit, tc.fallback), tc.want)
	}
}

// A plain go test build stamps nothing, so Short is never empty.
func TestShort(t *testing.T) {
	t.Parallel()

	testutil.True(t, "a version", version.Short() != "")
}
