package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
)

// snapshotGrace is how long a snapshot stays on disk after a newer commit
// replaced it, so an operation that loaded the fleet from it can finish
// reading it. Helm reads the values files when a deploy starts, and a
// restore reads them while it plans, so minutes are plenty.
const snapshotGrace = time.Hour

// The modes of the snapshots: readable by the server only.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// clone is the fleet clone as the server reads it. A sync hard-resets the Git
// clone in place, so nothing reads the clone itself: after every sync and
// commit, the tree of the new head is copied into a snapshot directory named
// after the commit, and Dir hands out the newest snapshot. An operation that
// loaded the fleet from an older snapshot keeps reading consistent files
// while the clone moves on. Promote's edit is the one thing that works in
// the clone, inside Repo.Commit's own lock.
type clone struct {
	repo *gitrepo.Repo
	root string
	log  *slog.Logger
	now  func() time.Time

	// mu serializes Sync and Commit with the snapshot each one takes, and
	// guards current and retired.
	mu      sync.Mutex
	current string
	retired []retiredSnapshot
}

// retiredSnapshot is a snapshot a newer commit replaced, and when.
type retiredSnapshot struct {
	dir string
	at  time.Time
}

// newClone snapshots the repository's head into root and returns the clone.
func newClone(repo *gitrepo.Repo, root string, log *slog.Logger) (*clone, error) {
	err := os.MkdirAll(root, dirMode)
	if err != nil {
		return nil, fmt.Errorf("making the snapshot directory: %w", err)
	}

	c := &clone{
		repo: repo, root: root, log: log, now: time.Now,
		mu: sync.Mutex{}, current: "", retired: nil,
	}

	err = c.removeStale()
	if err != nil {
		return nil, err
	}

	err = c.snapshot()
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Sync fetches the branch, resets the clone to it and snapshots the head.
func (c *clone) Sync(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sha, err := c.repo.Sync(ctx)
	if err != nil {
		return "", err
	}

	return sha, c.snapshot()
}

// Commit runs edit in the clone, commits and pushes, and snapshots the head
// it leaves, which is the remote's own head when the push failed.
func (c *clone) Commit(ctx context.Context, msg string, author gitrepo.Signature, edit gitrepo.Edit) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sha, err := c.repo.Commit(ctx, msg, author, edit)

	return sha, errors.Join(err, c.snapshot())
}

// Head is the commit the clone is at.
func (c *clone) Head() string {
	return c.repo.Head()
}

// FileLog lists the last n commits that changed path, relative to the
// fleet; a snapshot and the clone share their layout.
func (c *clone) FileLog(ctx context.Context, path string, n int) ([]gitrepo.LogEntry, error) {
	return c.repo.FileLog(ctx, path, n)
}

// CommitAuthor returns the author of a commit the clone has, for the
// release history; snapshots have no .git to look in.
func (c *clone) CommitAuthor(ctx context.Context, sha string) (gitrepo.Signature, error) {
	return c.repo.CommitAuthor(ctx, sha)
}

// Dir is the snapshot of the newest commit, which holds the fleet.
func (c *clone) Dir() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.current
}

// snapshot copies the clone's head into its snapshot directory, unless it is
// already there, makes it current and removes snapshots past their grace.
// The caller holds mu.
func (c *clone) snapshot() error {
	head := c.repo.Head()
	if head == "" {
		return nil
	}

	dir := filepath.Join(c.root, head)
	if dir == c.current {
		c.prune()

		return nil
	}

	_, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		err = c.copyTree(dir)
	}

	if err != nil {
		return fmt.Errorf("snapshotting commit %s: %w", head, err)
	}

	if c.current != "" {
		c.retired = append(c.retired, retiredSnapshot{dir: c.current, at: c.now()})
	}

	c.current = dir
	c.prune()

	return nil
}

// copyTree copies the clone's files, without .git, into a temporary
// directory and renames it to dir, so a snapshot is whole or absent.
func (c *clone) copyTree(dir string) error {
	tmp, err := os.MkdirTemp(c.root, ".partial-")
	if err != nil {
		return fmt.Errorf("making a temporary snapshot: %w", err)
	}

	err = copyDir(c.repo.Dir(), tmp)
	if err == nil {
		err = os.Rename(tmp, dir)
	}

	if err != nil {
		return errors.Join(err, os.RemoveAll(tmp))
	}

	return nil
}

// prune removes the snapshots retired longer than snapshotGrace ago, unless
// one became current again. The caller holds mu.
func (c *clone) prune() {
	keep := c.retired[:0]

	for _, r := range c.retired {
		switch {
		case r.dir == c.current:
		case c.now().Sub(r.at) < snapshotGrace:
			keep = append(keep, r)
		default:
			err := os.RemoveAll(r.dir)
			if err != nil {
				c.log.Warn("removing an old snapshot of the fleet", "dir", r.dir, "error", err)
			}
		}
	}

	c.retired = keep
}

// removeStale removes what an earlier run of the server left in root: its
// snapshots are of commits nothing reads any more.
func (c *clone) removeStale() error {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return fmt.Errorf("reading the snapshot directory: %w", err)
	}

	var errs []error

	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(c.root, e.Name())))
	}

	err = errors.Join(errs...)
	if err != nil {
		return fmt.Errorf("removing old snapshots: %w", err)
	}

	return nil
}

// copyDir copies the regular files and directories under src to dst, which
// exists, leaving out .git. Symbolic links are left out too: a fleet holds
// values files, and a link could point out of the clone.
func copyDir(src, dst string) error {
	//nolint:wrapcheck // copyFile and the walk's own errors name the path
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		switch {
		case rel == ".":
			return nil
		case d.IsDir() && d.Name() == ".git":
			return filepath.SkipDir
		case d.IsDir():
			return os.Mkdir(filepath.Join(dst, rel), dirMode)
		case d.Type().IsRegular():
			return copyFile(path, filepath.Join(dst, rel))
		default:
			return nil
		}
	})
}

// copyFile copies one regular file, readable by the server only.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is a file of the server's own clone
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close() //nolint:errcheck // a read-only file; the copy's errors are what matter

	//nolint:gosec // dst is under the snapshot root
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	_, err = io.Copy(out, in)

	return errors.Join(err, out.Close())
}
