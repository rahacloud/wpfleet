package gitrepo_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	origin   = "origin"
	nowhere  = "file:///nowhere"
	blogFile = "environments/staging/blog.yaml"
	tagOne   = "image:\n  tag: one\n"
)

//nolint:gochecknoglobals // fixed test identities, read only
var (
	bot    = gitrepo.Signature{Name: "wpfleet", Email: "wpfleet@example.com"}
	person = gitrepo.Signature{Name: "Sara Editor", Email: "sara@example.com"}
)

// readBlog reads blog's staging file in the clone.
func readBlog(t *testing.T, r *gitrepo.Repo) string {
	t.Helper()

	return testutil.ReadFile(t, filepath.Join(r.Dir(), blogFile))
}

// remote makes a bare repository with one commit on main holding the files,
// and returns its file:// URL.
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

	//nolint:exhaustruct_v5 // a plain non-bare repository with go-git's defaults
	repo, err := git.PlainInitWithOptions(work, &git.PlainInitOptions{
		DefaultBranch: plumbing.NewBranchReferenceName("main"),
	})
	testutil.NoError(t, err)

	//nolint:exhaustruct_v5 // only the name and the URL matter for a test remote
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: origin, URLs: []string{"file://" + bare}})
	testutil.NoError(t, err)

	wt, err := repo.Worktree()
	testutil.NoError(t, err)

	for name, content := range files {
		testutil.WriteFile(t, work, name, content)
		_, err = wt.Add(name)
		testutil.NoError(t, err)
	}

	commit(t, wt, "Start the fleet")

	//nolint:exhaustruct_v5 // a plain push of the default refspec
	err = repo.Push(&git.PushOptions{RemoteName: origin})
	testutil.NoError(t, err)

	return "file://" + bare
}

// commit commits the staged changes as someone else than the server.
func commit(t *testing.T, wt *git.Worktree, msg string) plumbing.Hash {
	t.Helper()

	sig := &object.Signature{Name: "Other", Email: "other@example.com", When: time.Now()}

	//nolint:exhaustruct_v5 // an ordinary commit
	hash, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig})
	testutil.NoError(t, err)

	return hash
}

// pushFile writes one file from another clone of url and pushes it, the way
// a person or CI moves the branch, and returns the new head.
func pushFile(t *testing.T, url, name, content string) string {
	t.Helper()

	work := t.TempDir()

	//nolint:exhaustruct_v5 // a full clone of the default branch
	repo, err := git.PlainClone(work, false, &git.CloneOptions{URL: url})
	testutil.NoError(t, err)

	wt, err := repo.Worktree()
	testutil.NoError(t, err)

	testutil.WriteFile(t, work, name, content)
	_, err = wt.Add(name)
	testutil.NoError(t, err)

	hash := commit(t, wt, "Change "+name)

	//nolint:exhaustruct_v5 // a plain push of the default refspec
	err = repo.Push(&git.PushOptions{RemoteName: origin})
	testutil.NoError(t, err)

	return hash.String()
}

// remoteHead reads the branch head of the bare repository behind url.
func remoteHead(t *testing.T, url string) *object.Commit {
	t.Helper()

	repo, err := git.PlainOpen(url[len("file://"):])
	testutil.NoError(t, err)

	ref, err := repo.Reference(plumbing.NewBranchReferenceName("main"), true)
	testutil.NoError(t, err)

	c, err := repo.CommitObject(ref.Hash())
	testutil.NoError(t, err)

	return c
}

func open(t *testing.T, url string) *gitrepo.Repo {
	t.Helper()

	//nolint:exhaustruct_v5 // no credentials for a file:// remote
	r, err := gitrepo.Open(t.Context(), gitrepo.Config{
		URL:       url,
		Branch:    "main",
		Dir:       filepath.Join(t.TempDir(), "clone"),
		Committer: bot,
	})
	testutil.NoError(t, err)

	return r
}

func site(content string) map[string]string {
	return map[string]string{"wpfleet.yaml": "environments: []\n", blogFile: content}
}

func TestOpenClones(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	testutil.Equal(t, "head", r.Head(), remoteHead(t, url).Hash.String())
	testutil.Equal(t, "site file", readBlog(t, r), tagOne)
}

func TestOpenExistingClone(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)
	moved := pushFile(t, url, blogFile, "image:\n  tag: two\n")

	//nolint:exhaustruct_v5 // no credentials for a file:// remote
	again, err := gitrepo.Open(t.Context(), gitrepo.Config{URL: url, Dir: r.Dir(), Committer: bot})
	testutil.NoError(t, err)
	testutil.Equal(t, "head after reopening", again.Head(), moved)

	other := remote(t, site(""))

	//nolint:exhaustruct_v5 // no credentials for a file:// remote
	_, err = gitrepo.Open(t.Context(), gitrepo.Config{URL: other, Dir: r.Dir(), Committer: bot})
	testutil.ErrorIs(t, err, gitrepo.ErrOtherRemote)
}

func TestOpenInvalid(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]gitrepo.Config{
		"no url": {URL: "", Branch: "", Dir: t.TempDir(), SSHKeyFile: "", Token: "", Committer: bot},
		"no dir": {URL: nowhere, Branch: "", Dir: "", SSHKeyFile: "", Token: "", Committer: bot},
		"no committer": {
			URL: nowhere, Branch: "", Dir: t.TempDir(), SSHKeyFile: "", Token: "",
			Committer: gitrepo.Signature{Name: "", Email: ""},
		},
		"bad branch": {URL: nowhere, Branch: "a..b", Dir: t.TempDir(), SSHKeyFile: "", Token: "", Committer: bot},
		"key and token": {
			URL: "https://git.example.com/fleet.git", Branch: "", Dir: t.TempDir(),
			SSHKeyFile: "/key", Token: "t", Committer: bot,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := gitrepo.Open(t.Context(), c)
			testutil.ErrorIs(t, err, gitrepo.ErrInvalid)
		})
	}
}

func TestSync(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	// Whatever is in the clone and not on the remote goes.
	testutil.WriteFile(t, r.Dir(), blogFile, "edited by hand\n")
	testutil.WriteFile(t, r.Dir(), "stray/file.yaml", "untracked\n")

	moved := pushFile(t, url, "environments/staging/shop.yaml", "image:\n  tag: s\n")

	sha, err := r.Sync(t.Context())
	testutil.NoError(t, err)
	testutil.Equal(t, "sync result", sha, moved)
	testutil.Equal(t, "head", r.Head(), moved)
	testutil.Equal(t, "reset file", readBlog(t, r), tagOne)
	shop := testutil.ReadFile(t, filepath.Join(r.Dir(), "environments/staging/shop.yaml"))
	testutil.Equal(t, "new file", shop, "image:\n  tag: s\n")

	_, err = os.Stat(filepath.Join(r.Dir(), "stray"))
	testutil.True(t, "untracked directory removed", os.IsNotExist(err))
}

// setTag is an edit that writes the tag into blog's staging file.
func setTag(tag string) gitrepo.Edit {
	return func(dir string) ([]string, error) {
		path := filepath.Join(dir, blogFile)

		return []string{path}, os.WriteFile(path, []byte("image:\n  tag: "+tag+"\n"), 0o600)
	}
}

func TestCommit(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	sha, err := r.Commit(t.Context(), "Promote blog to staging", person, setTag("two"))
	testutil.NoError(t, err)

	head := remoteHead(t, url)
	testutil.Equal(t, "pushed", head.Hash.String(), sha)
	testutil.Equal(t, "clone head", r.Head(), sha)
	testutil.Equal(t, "author", head.Author.Email, person.Email)
	testutil.Equal(t, "committer", head.Committer.Email, bot.Email)
	testutil.Equal(t, "message", head.Message, "Promote blog to staging")

	file, err := head.File(blogFile)
	testutil.NoError(t, err)

	content, err := file.Contents()
	testutil.NoError(t, err)
	testutil.Equal(t, "committed content", content, "image:\n  tag: two\n")
}

func TestCommitWithoutAuthorUsesCommitter(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	_, err := r.Commit(t.Context(), "Promote", gitrepo.Signature{Name: "", Email: ""}, setTag("two"))
	testutil.NoError(t, err)
	testutil.Equal(t, "author", remoteHead(t, url).Author.Email, bot.Email)
}

func TestCommitDeletesFile(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	_, err := r.Commit(t.Context(), "Remove blog", person, func(dir string) ([]string, error) {
		return []string{blogFile}, os.Remove(filepath.Join(dir, blogFile))
	})
	testutil.NoError(t, err)

	_, err = remoteHead(t, url).File(blogFile)
	testutil.ErrorIs(t, err, object.ErrFileNotFound)
}

func TestCommitRetriesWhenTheBranchMoves(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	var calls int

	sha, err := r.Commit(t.Context(), "Promote blog", person, func(dir string) ([]string, error) {
		calls++
		if calls == 1 {
			// Someone pushes after the clone synced and before it pushes.
			pushFile(t, url, "environments/staging/shop.yaml", "image:\n  tag: s\n")
		}

		return setTag("two")(dir)
	})
	testutil.NoError(t, err)
	testutil.Equal(t, "edit runs", calls, 2)

	head := remoteHead(t, url)
	testutil.Equal(t, "pushed", head.Hash.String(), sha)

	parent, err := head.Parent(0)
	testutil.NoError(t, err)
	testutil.Equal(t, "built on the other push", parent.Message, "Change environments/staging/shop.yaml")
}

func TestCommitRefusedTwice(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	var calls int

	_, err := r.Commit(t.Context(), "Promote blog", person, func(dir string) ([]string, error) {
		calls++
		pushFile(t, url, "environments/staging/shop.yaml", "image:\n  tag: "+strconv.Itoa(calls)+"\n")
		testutil.WriteFile(t, dir, "leftover.yaml", "not committed\n")

		return setTag("two")(dir)
	})
	testutil.ErrorIs(t, err, gitrepo.ErrPushRefused)
	testutil.Equal(t, "edit runs", calls, 2)

	// The clone is back at the remote, with nothing of the edit left.
	head := remoteHead(t, url)
	testutil.Equal(t, "clone head", r.Head(), head.Hash.String())
	testutil.Equal(t, "site file", readBlog(t, r), tagOne)

	_, err = os.Stat(filepath.Join(r.Dir(), "leftover.yaml"))
	testutil.True(t, "untracked file removed", os.IsNotExist(err))
}

func TestCommitEditFails(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)
	before := r.Head()

	_, err := r.Commit(t.Context(), "Promote", person, func(dir string) ([]string, error) {
		_, err := setTag("two")(dir)
		testutil.NoError(t, err)

		return nil, os.ErrPermission
	})
	testutil.ErrorIs(t, err, os.ErrPermission)
	testutil.Equal(t, "head", r.Head(), before)
	testutil.Equal(t, "site file", readBlog(t, r), tagOne)
}

func TestCommitNothing(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	_, err := r.Commit(t.Context(), "Promote", person, setTag("one"))
	testutil.ErrorIs(t, err, gitrepo.ErrNothingToCommit)

	_, err = r.Commit(t.Context(), "Promote", person, func(string) ([]string, error) { return nil, nil })
	testutil.ErrorIs(t, err, gitrepo.ErrNothingToCommit)
}

func TestCommitOutsideClone(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	for _, path := range []string{"../outside.yaml", filepath.Join(t.TempDir(), "outside.yaml"), "."} {
		_, err := r.Commit(t.Context(), "Promote", person, func(string) ([]string, error) { return []string{path}, nil })
		testutil.ErrorIs(t, err, gitrepo.ErrOutsideClone)
	}
}

func TestCommitCanceled(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.Commit(ctx, "Promote", person, setTag("two"))
	testutil.Error(t, err, "commit with a canceled context")
	testutil.Equal(t, "remote unchanged", remoteHead(t, url).Message, "Start the fleet")
}

func TestFileLog(t *testing.T) {
	t.Parallel()

	url := remote(t, site(tagOne))
	r := open(t, url)

	first, err := r.Commit(t.Context(), "Promote blog to two", person, setTag("two"))
	testutil.NoError(t, err)

	pushFile(t, url, "environments/staging/shop.yaml", "image:\n  tag: s\n")

	second, err := r.Commit(t.Context(), "Promote blog to three\n\nWith a body.", person, setTag("three"))
	testutil.NoError(t, err)

	log, err := r.FileLog(t.Context(), blogFile, 10)
	testutil.NoError(t, err)
	testutil.Equal(t, "entries", len(log), 3)
	testutil.Equal(t, "newest", log[0].SHA, second)
	testutil.Equal(t, "subject", log[0].Subject, "Promote blog to three")
	testutil.Equal(t, "author", log[0].Email, person.Email)
	testutil.Equal(t, "author name", log[0].Author, person.Name)
	testutil.Equal(t, "second", log[1].SHA, first)
	testutil.Equal(t, "oldest", log[2].Subject, "Start the fleet")

	limited, err := r.FileLog(t.Context(), filepath.Join(r.Dir(), blogFile), 1)
	testutil.NoError(t, err)
	testutil.Equal(t, "limited", len(limited), 1)

	none, err := r.FileLog(t.Context(), "environments/production/blog.yaml", 10)
	testutil.NoError(t, err)
	testutil.Equal(t, "untouched path", len(none), 0)

	_, err = r.FileLog(t.Context(), "../x", 10)
	testutil.ErrorIs(t, err, gitrepo.ErrOutsideClone)
}
