package serve_test

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/cmd/serve"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

func newTestClone(t *testing.T, url string, now func() time.Time) (*serve.Clone, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "snapshots")

	c, err := serve.NewClone(openRepo(t, url), root, slog.New(slog.DiscardHandler), now)
	testutil.NoError(t, err)

	return c, root
}

func exists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	testutil.NoError(t, err)

	return true
}

func TestSnapshotHoldsTheFleetWithoutGit(t *testing.T) {
	t.Parallel()

	c, root := newTestClone(t, remote(t, fleetFiles()), time.Now)

	dir := c.Dir()
	testutil.Equal(t, "snapshot is the head", dir, filepath.Join(root, c.Head()))
	testutil.Equal(t, "site file", testutil.ReadFile(t, filepath.Join(dir, blogFile)),
		"site: blog\n")
	testutil.True(t, "no .git in the snapshot", !exists(t, filepath.Join(dir, ".git")))
}

// Dir must not wait for a Sync or a Commit: they hold the clone's lock
// through the network, and every page, the sign-in page included, reads Dir
// for the brand's images.
func TestDirDoesNotWaitForASync(t *testing.T) {
	t.Parallel()

	c, root := newTestClone(t, remote(t, fleetFiles()), time.Now)

	release := c.Hold()
	defer release()

	got := make(chan string, 1)

	go func() { got <- c.Dir() }()

	select {
	case dir := <-got:
		testutil.Equal(t, "Dir during a sync", dir, filepath.Join(root, c.Head()))
	case <-time.After(5 * time.Second):
		t.Fatal("Dir waited for the lock a sync holds")
	}
}

func TestSyncMovesToANewSnapshotAndKeepsTheOld(t *testing.T) {
	t.Parallel()

	clk := &clock{mu: sync.Mutex{}, now: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)}
	url := remote(t, fleetFiles())
	c, root := newTestClone(t, url, clk.Now)
	old := c.Dir()

	head := pushFile(t, url, blogFile, "site: blog\nimage: {tag: v2}\n")

	sha, err := c.Sync(t.Context())
	testutil.NoError(t, err)
	testutil.Equal(t, "synced head", sha, head)
	testutil.Equal(t, "current snapshot", c.Dir(), filepath.Join(root, head))
	testutil.Contains(t, "new content", testutil.ReadFile(t, filepath.Join(c.Dir(), blogFile)),
		"v2")
	testutil.Equal(t, "old snapshot unchanged", testutil.ReadFile(t, filepath.Join(old, blogFile)),
		"site: blog\n")

	clk.Add(serve.SnapshotWait + time.Minute)

	_, err = c.Sync(t.Context())
	testutil.NoError(t, err)
	testutil.True(t, "old snapshot removed after its grace", !exists(t, old))
	testutil.True(t, "current snapshot kept", exists(t, c.Dir()))
}

func TestCommitSnapshotsWhatItPushed(t *testing.T) {
	t.Parallel()

	c, _ := newTestClone(t, remote(t, fleetFiles()), time.Now)

	sha, err := c.Commit(t.Context(), "Change blog", bot, func(dir string) ([]string, error) {
		return []string{blogFile},
			os.WriteFile(filepath.Join(dir, blogFile), []byte("site: blog\n# changed\n"), 0o600)
	})
	testutil.NoError(t, err)
	testutil.Equal(t, "head", c.Head(), sha)
	testutil.Contains(t, "snapshot of the commit",
		testutil.ReadFile(t, filepath.Join(c.Dir(), blogFile)),
		"# changed")

	log, err := c.FileLog(t.Context(), blogFile, 1)
	testutil.NoError(t, err)
	testutil.Equal(t, "history", log[0].SHA, sha)
}

func TestNewCloneRemovesStaleSnapshots(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "snapshots")
	testutil.WriteFile(t, root, "0123/wpfleet.yaml", "environments: []\n")

	_, err := serve.NewClone(openRepo(t, remote(t, fleetFiles())), root, slog.New(slog.DiscardHandler), time.Now)
	testutil.NoError(t, err)
	testutil.True(t, "stale snapshot removed", !exists(t, filepath.Join(root, "0123")))
}

func TestFailedCommitLeavesTheSnapshot(t *testing.T) {
	t.Parallel()

	c, _ := newTestClone(t, remote(t, fleetFiles()), time.Now)
	before := c.Dir()

	_, err := c.Commit(t.Context(), "Nothing", bot, func(string) ([]string, error) { return nil, nil })
	testutil.ErrorIs(t, err, gitrepo.ErrNothingToCommit)
	testutil.Equal(t, "snapshot", c.Dir(), before)
}
