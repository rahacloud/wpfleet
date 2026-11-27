package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrPushRefused is returned by Commit when the remote refused the push
// twice because the branch moved both times.
var ErrPushRefused = errors.New("the push was refused twice because the branch kept moving")

// ErrNothingToCommit is returned by Commit when the edit changed nothing.
var ErrNothingToCommit = errors.New("nothing to commit")

// ErrOutsideClone is returned by Commit for an edited path outside the
// clone.
var ErrOutsideClone = errors.New("path is outside the clone")

// errMoved marks a push the remote refused because its branch had moved
// past the commit the change was made on.
var errMoved = errors.New("the branch moved")

// Edit changes files in the clone at dir and returns the paths it changed,
// absolute or relative to dir. Commit runs it, and runs it again on the new
// head when the first push is refused, so it must plan from what is on disk
// rather than from what it saw the first time.
type Edit func(dir string) ([]string, error)

// Commit makes one change on top of the remote branch and pushes it: it
// syncs the clone, runs edit, commits the paths it returns with author as
// the author and the configured committer, and pushes. When the push is
// refused because the branch moved, it syncs again, runs edit again and
// pushes once more; a second refusal returns ErrPushRefused. Any failure
// leaves the clone reset to the remote, so nothing half-done stays behind.
// It returns the pushed commit's SHA. Commits run one at a time.
func (r *Repo) Commit(ctx context.Context, msg string, author Signature, edit Edit) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sha, err := r.attempt(ctx, msg, author, edit)
	if errors.Is(err, errMoved) {
		sha, err = r.attempt(ctx, msg, author, edit)
		if errors.Is(err, errMoved) {
			err = fmt.Errorf("%w: %w", ErrPushRefused, err)
		}
	}

	if err != nil {
		r.discard(ctx)

		return "", err
	}

	return sha, nil
}

// attempt syncs, edits, commits and pushes once. Its error wraps errMoved
// when the remote refused the push because the branch had moved.
func (r *Repo) attempt(ctx context.Context, msg string, author Signature, edit Edit) (string, error) {
	err := r.sync(ctx)
	if err != nil {
		return "", err
	}

	base := r.head

	paths, err := edit(r.cfg.Dir)
	if err != nil {
		return "", err
	}

	hash, err := r.commit(msg, author, paths)
	if err != nil {
		return "", err
	}

	err = r.push(ctx)
	if err != nil {
		remote, fetchErr := r.fetch(ctx)
		if fetchErr == nil && remote != base {
			return "", fmt.Errorf("%w: pushing to %s: %w", errMoved, r.cfg.Branch, err)
		}

		return "", fmt.Errorf("pushing to %s: %w", r.cfg.Branch, err)
	}

	r.head = hash

	return hash.String(), nil
}

// commit stages paths and commits them.
func (r *Repo) commit(msg string, author Signature, paths []string) (plumbing.Hash, error) {
	if len(paths) == 0 {
		return plumbing.ZeroHash, ErrNothingToCommit
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("opening the worktree: %w", err)
	}

	for _, p := range paths {
		err = r.stage(wt, p)
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}

	if author.Name == "" || author.Email == "" {
		author = r.cfg.Committer
	}

	now := time.Now()

	//nolint:exhaustruct_v5 // no signing key, no parents override, no amend: an ordinary commit on HEAD
	hash, err := wt.Commit(msg, &git.CommitOptions{
		Author:    &object.Signature{Name: author.Name, Email: author.Email, When: now},
		Committer: &object.Signature{Name: r.cfg.Committer.Name, Email: r.cfg.Committer.Email, When: now},
	})
	if errors.Is(err, git.ErrEmptyCommit) {
		return plumbing.ZeroHash, ErrNothingToCommit
	}

	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("committing: %w", err)
	}

	return hash, nil
}

// stage adds one changed path to the index, or removes it when the edit
// deleted the file.
func (r *Repo) stage(wt *git.Worktree, path string) error {
	rel, err := r.relative(path)
	if err != nil {
		return err
	}

	_, err = os.Lstat(filepath.Join(r.cfg.Dir, rel))

	switch {
	case errors.Is(err, os.ErrNotExist):
		_, err = wt.Remove(filepath.ToSlash(rel))
	case err != nil:
		return fmt.Errorf("reading %s: %w", rel, err)
	default:
		_, err = wt.Add(filepath.ToSlash(rel))
	}

	if err != nil {
		return fmt.Errorf("staging %s: %w", rel, err)
	}

	return nil
}

// relative makes path relative to the clone and refuses one outside it.
func (r *Repo) relative(path string) (string, error) {
	rel := path

	if filepath.IsAbs(path) {
		var err error

		rel, err = filepath.Rel(r.cfg.Dir, path)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrOutsideClone, path, err)
		}
	}

	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: %s", ErrOutsideClone, path)
	}

	return rel, nil
}

// push pushes the local branch to the remote one, never forcing.
func (r *Repo) push(ctx context.Context) error {
	branch := plumbing.NewBranchReferenceName(r.cfg.Branch)
	spec := gitconfig.RefSpec(fmt.Sprintf("%s:%s", branch, branch))

	//nolint:exhaustruct_v5 // no force, no prune, no options: a plain push of one branch
	err := r.repo.PushContext(ctx, &git.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []gitconfig.RefSpec{spec},
		Auth:       r.auth,
	})
	if err != nil {
		return fmt.Errorf("pushing: %w", err)
	}

	return nil
}

// discard puts the clone back at the remote after a failed change. When the
// remote cannot be reached it falls back to the last head it knew, so a
// local commit that was never pushed never stays in the clone.
func (r *Repo) discard(ctx context.Context) {
	if r.sync(ctx) == nil {
		return
	}

	tracking := plumbing.NewRemoteReferenceName(remoteName, r.cfg.Branch)

	ref, err := r.repo.Reference(tracking, true)
	// A best effort after a failure that is already being returned.
	if err != nil {
		_ = r.resetTo(r.head)

		return
	}

	_ = r.resetTo(ref.Hash())
}
