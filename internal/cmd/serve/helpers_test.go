package serve_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// The names the tests share.
const (
	siteBlog      = "blog"
	envStaging    = "staging"
	groupPlatform = "platform"
	groupShop     = "shop-team"
	blogFile      = "environments/staging/blog.yaml"
)

// bot is the server's committer in the tests.
//
//nolint:gochecknoglobals // a test constant of struct type
var bot = gitrepo.Signature{Name: "wpfleet", Email: "wpfleet@example.com"}

// unreachableKubeconfig points at a port nothing listens on, so a test that
// reaches for a cluster fails at once instead of finding a real one.
const unreachableKubeconfig = `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://127.0.0.1:1"}}]
users: [{name: u, user: {token: not-a-real-token}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`

// fleetFiles is a fleet with blog in staging, on an unreachable cluster.
func fleetFiles() map[string]string {
	return map[string]string{
		"wpfleet.yaml": "environments:\n" +
			"  - name: staging\n    namespace: wp-staging\n    kubeconfig: kubeconfig\n",
		"kubeconfig":                        unreachableKubeconfig,
		"environments/staging/_shared.yaml": "{}\n",
		blogFile:                            "site: blog\n",
	}
}

// remote makes a bare repository whose main branch holds files, and returns
// its file:// URL.
func remote(t *testing.T, files map[string]string) string {
	t.Helper()

	bare := filepath.Join(t.TempDir(), "fleet.git")

	//nolint:exhaustruct_v5 // a bare repository with go-git's defaults
	_, err := git.PlainInitWithOptions(bare, &git.PlainInitOptions{
		DefaultBranch: plumbing.NewBranchReferenceName("main"),
		Bare:          true,
	})
	testutil.NoError(t, err)

	work := t.TempDir()

	//nolint:exhaustruct_v5 // a work tree with go-git's defaults
	repo, err := git.PlainInitWithOptions(work, &git.PlainInitOptions{
		DefaultBranch: plumbing.NewBranchReferenceName("main"),
	})
	testutil.NoError(t, err)

	//nolint:exhaustruct_v5 // only the name and the URL matter for a test remote
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"file://" + bare}})
	testutil.NoError(t, err)

	commitFiles(t, repo, work, files)

	return "file://" + bare
}

// pushFile changes one file from another clone of url and pushes it, as a
// person would, and returns the new head.
func pushFile(t *testing.T, url, name, content string) string {
	t.Helper()

	work := t.TempDir()

	//nolint:exhaustruct_v5 // a full clone of the default branch
	repo, err := git.PlainClone(work, false, &git.CloneOptions{URL: url})
	testutil.NoError(t, err)

	return commitFiles(t, repo, work, map[string]string{name: content})
}

// commitFiles writes files in work, commits them and pushes to origin.
func commitFiles(t *testing.T, repo *git.Repository, work string, files map[string]string) string {
	t.Helper()

	wt, err := repo.Worktree()
	testutil.NoError(t, err)

	for name, content := range files {
		testutil.WriteFile(t, work, name, content)
		_, err = wt.Add(name)
		testutil.NoError(t, err)
	}

	sig := &object.Signature{Name: "Someone", Email: "someone@example.com", When: time.Now()}

	//nolint:exhaustruct_v5 // an ordinary commit
	hash, err := wt.Commit("Change the fleet", &git.CommitOptions{Author: sig, Committer: sig})
	testutil.NoError(t, err)

	//nolint:exhaustruct_v5 // a plain push of the default refspec
	err = repo.Push(&git.PushOptions{RemoteName: "origin"})
	testutil.NoError(t, err)

	return hash.String()
}

// openRepo clones url for the server.
func openRepo(t *testing.T, url string) *gitrepo.Repo {
	t.Helper()

	//nolint:exhaustruct_v5 // no credentials for a file:// remote
	r, err := gitrepo.Open(t.Context(), gitrepo.Config{
		URL: url, Branch: "main", Dir: filepath.Join(t.TempDir(), "fleet"), Committer: bot,
	})
	testutil.NoError(t, err)

	return r
}
