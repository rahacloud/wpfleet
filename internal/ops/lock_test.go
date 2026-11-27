package ops_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestLocks(t *testing.T) {
	t.Parallel()

	var locks ops.Locks

	unlock, err := locks.TryLock("staging", "blog", "deploy")
	testutil.NoError(t, err)

	running, ok := locks.Running("staging", "blog")
	testutil.True(t, "held", ok && running == "deploy")

	_, err = locks.TryLock("staging", "blog", "restore")
	testutil.ErrorIs(t, err, ops.ErrBusy)

	busy, ok := errors.AsType[*ops.BusyError](err)
	testutil.True(t, "busy error", ok)
	testutil.Equal(t, "running action", busy.Action, "deploy")
	testutil.Equal(t, "message", err.Error(), "blog in staging: deploy is running")

	// Another site, or the same site in another environment, is free.
	other, err := locks.TryLock("staging", "shop", "backup")
	testutil.NoError(t, err)
	other()

	prod, err := locks.TryLock("production", "blog", "backup")
	testutil.NoError(t, err)
	prod()

	unlock()
	unlock() // a second release does nothing

	again, err := locks.TryLock("staging", "blog", "restore")
	testutil.NoError(t, err)
	again()

	_, ok = locks.Running("staging", "blog")
	testutil.True(t, "released", !ok)
}

// Of many concurrent attempts on one site, exactly one gets the lock.
func TestLocksConcurrent(t *testing.T) {
	t.Parallel()

	var (
		locks ops.Locks
		mu    sync.Mutex
		got   int
		wg    sync.WaitGroup
	)

	for range 50 {
		wg.Go(func() {
			_, err := locks.TryLock("staging", "blog", "deploy")
			if err == nil {
				mu.Lock()
				got++
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	testutil.Equal(t, "holders", got, 1)
}
