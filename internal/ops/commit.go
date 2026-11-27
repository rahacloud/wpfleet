package ops

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// DescriptionPrefix starts the Helm release description of every deploy.
const DescriptionPrefix = "wpfleet"

// dirtySuffix marks a commit whose tree had uncommitted changes.
const dirtySuffix = "-dirty"

// Commit is the fleet commit a deploy was made from.
type Commit struct {
	// SHA is the full hash of the commit HEAD points at; empty when the
	// fleet is not in a Git repository or has no commit yet.
	SHA string `json:"sha"`
	// Dirty says whether the fleet directory had uncommitted changes,
	// untracked files included.
	Dirty bool `json:"dirty"`
}

// String is the commit as a release description records it: the SHA, with
// -dirty when the tree had changes, or empty.
func (c Commit) String() string {
	if c.SHA == "" {
		return ""
	}

	if c.Dirty {
		return c.SHA + dirtySuffix
	}

	return c.SHA
}

// Description is the Helm release description of a deploy from c:
// "wpfleet <sha>", "wpfleet <sha>-dirty", or "wpfleet" without a commit.
func (c Commit) Description() string {
	if c.SHA == "" {
		return DescriptionPrefix
	}

	return DescriptionPrefix + " " + c.String()
}

// ParseDescription reads the commit back from a release description that
// Description wrote. It returns false for a description wpfleet did not
// write, or one without a commit.
func ParseDescription(description string) (Commit, bool) {
	rest, ok := strings.CutPrefix(description, DescriptionPrefix+" ")
	if !ok || rest == "" || strings.ContainsAny(rest, " \n") {
		return Commit{SHA: "", Dirty: false}, false
	}

	sha, dirty := strings.CutSuffix(rest, dirtySuffix)

	return Commit{SHA: sha, Dirty: dirty}, true
}

// FleetCommit reads the commit of the Git repository that holds the fleet
// directory dir, with go-git, so it needs no git binary. Only changes under
// dir make the commit dirty. A fleet outside any repository, or in one with
// no commit, gives the zero Commit and no error.
func FleetCommit(dir string) (Commit, error) {
	none := Commit{SHA: "", Dirty: false}

	//nolint:exhaustruct_v5 // the common-dir support is off, as in git's default layout
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return none, nil
	}

	if err != nil {
		return none, fmt.Errorf("opening the fleet repository: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		// A repository without a commit has no HEAD to point at.
		return none, nil //nolint:nilerr // no commit is not a failure of the deploy
	}

	wt, err := repo.Worktree()
	if err != nil {
		return none, fmt.Errorf("opening the fleet worktree: %w", err)
	}

	wt.Excludes = append(wt.Excludes, userExcludes()...)

	status, err := wt.Status()
	if err != nil {
		return none, fmt.Errorf("reading the fleet status: %w", err)
	}

	prefix, err := relativeTo(wt.Filesystem.Root(), dir)
	if err != nil {
		return none, err
	}

	return Commit{SHA: head.Hash().String(), Dirty: changedUnder(status, prefix)}, nil
}

// changedUnder says whether status has a change, untracked files included,
// under prefix.
func changedUnder(status git.Status, prefix string) bool {
	for path, s := range status {
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			continue
		}

		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			return true
		}
	}

	return false
}

// userExcludes are the ignore patterns of the user's and the system's Git
// configuration (core.excludesFile), which go-git's status leaves out: a
// file that only they ignore would otherwise make every deploy dirty. They
// are best effort; a configuration that cannot be read adds nothing.
func userExcludes() []gitignore.Pattern {
	root := osfs.New("/")

	global, err := gitignore.LoadGlobalPatterns(root)
	if err != nil {
		global = nil
	}

	system, err := gitignore.LoadSystemPatterns(root)
	if err != nil {
		system = nil
	}

	return append(global, system...)
}

// relativeTo is dir relative to root in Git's slash form, "" for root itself.
func relativeTo(root, dir string) (string, error) {
	r, err := realPath(root)
	if err != nil {
		return "", err
	}

	d, err := realPath(dir)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(r, d)
	if err != nil {
		return "", fmt.Errorf("locating the fleet in its repository: %w", err)
	}

	if rel == "." {
		return "", nil
	}

	return filepath.ToSlash(rel) + "/", nil
}

// realPath is the absolute path with symbolic links resolved.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", p, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", p, err)
	}

	return resolved, nil
}
