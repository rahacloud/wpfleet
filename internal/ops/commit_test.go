package ops_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// commitAll stages every file of the repository at dir and commits it.
func commitAll(t *testing.T, repo *git.Repository) string {
	t.Helper()

	wt, err := repo.Worktree()
	testutil.NoError(t, err)

	//nolint:exhaustruct_v5 // all files, nothing else
	testutil.NoError(t, wt.AddWithOptions(&git.AddOptions{All: true}))

	//nolint:exhaustruct_v5 // an author is all a test commit needs
	hash, err := wt.Commit("test", &git.CommitOptions{
		Author: &object.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1_800_000_000, 0)},
	})
	testutil.NoError(t, err)

	return hash.String()
}

func TestFleetCommit(t *testing.T) {
	t.Parallel()

	// A directory outside any repository has no commit.
	c, err := ops.FleetCommit(t.TempDir())
	testutil.NoError(t, err)
	testutil.Equal(t, "no repository", c.Description(), "wpfleet")

	root := t.TempDir()
	fleetDir := filepath.Join(root, "fleet")
	testutil.WriteFile(t, fleetDir, "wpfleet.yaml", "environments: []\n")
	testutil.WriteFile(t, root, "README.md", "a fleet in a subdirectory\n")

	repo, err := git.PlainInit(root, false)
	testutil.NoError(t, err)

	// A repository without a commit has none either.
	c, err = ops.FleetCommit(fleetDir)
	testutil.NoError(t, err)
	testutil.Equal(t, "no commit yet", c.Description(), "wpfleet")

	sha := commitAll(t, repo)

	c, err = ops.FleetCommit(fleetDir)
	testutil.NoError(t, err)
	testutil.Equal(t, "clean", c, ops.Commit{SHA: sha, Dirty: false})
	testutil.Equal(t, "description", c.Description(), "wpfleet "+sha)

	// A change outside the fleet directory does not make it dirty.
	testutil.WriteFile(t, root, "README.md", "changed\n")

	c, err = ops.FleetCommit(fleetDir)
	testutil.NoError(t, err)
	testutil.True(t, "a change outside the fleet", !c.Dirty)

	// A new site file nobody committed does.
	testutil.WriteFile(t, fleetDir, "environments/staging/blog.yaml", "site: blog\n")

	c, err = ops.FleetCommit(fleetDir)
	testutil.NoError(t, err)
	testutil.Equal(t, "dirty", c.Description(), "wpfleet "+sha+"-dirty")

	// From the repository's root, every change counts.
	c, err = ops.FleetCommit(root)
	testutil.NoError(t, err)
	testutil.True(t, "dirty at the root", c.Dirty)
}

func TestParseDescription(t *testing.T) {
	t.Parallel()

	sha := "0123456789abcdef0123456789abcdef01234567"

	for _, c := range []ops.Commit{{SHA: sha, Dirty: false}, {SHA: sha, Dirty: true}} {
		got, ok := ops.ParseDescription(c.Description())
		testutil.True(t, "parsed "+c.Description(), ok)
		testutil.Equal(t, "round trip", got, c)
	}

	for _, d := range []string{"wpfleet", "Upgrade complete", "wpfleet a b", "wpfleetx " + sha, ""} {
		_, ok := ops.ParseDescription(d)
		testutil.True(t, "not a commit: "+d, !ok)
	}
}
