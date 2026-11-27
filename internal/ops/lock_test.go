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

// beside lets a login run beside anything but a restore, the way the server
// allows it.
func beside(running, wanted string) bool {
	const login, restore = "login", "restore"

	return (running == login || wanted == login) && running != restore && wanted != restore
}

func TestLocksBeside(t *testing.T) {
	t.Parallel()

	locks := ops.Locks{Beside: beside}

	deploy, err := locks.TryLock("staging", "blog", "deploy")
	testutil.NoError(t, err)

	_, blocked := locks.Blocking("staging", "blog", "login")
	testutil.True(t, "a login is not blocked by a deploy", !blocked)

	login, err := locks.TryLock("staging", "blog", "login")
	testutil.NoError(t, err)

	second, err := locks.TryLock("staging", "blog", "login")
	testutil.NoError(t, err)

	running, _ := locks.Running("staging", "blog")
	testutil.Equal(t, "the first holder is running", running, "deploy")

	_, err = locks.TryLock("staging", "blog", "backup")
	testutil.ErrorIs(t, err, ops.ErrBusy)

	deploy()

	// The logins still hold the site, so a restore waits for them.
	by, blocked := locks.Blocking("staging", "blog", "restore")
	testutil.True(t, "a restore is blocked by a login", blocked && by == "login")

	_, err = locks.TryLock("staging", "blog", "restore")
	testutil.ErrorIs(t, err, ops.ErrBusy)

	login()
	login() // releases only its own entry, once

	running, _ = locks.Running("staging", "blog")
	testutil.Equal(t, "the other login holds it", running, "login")

	second()

	restore, err := locks.TryLock("staging", "blog", "restore")
	testutil.NoError(t, err)

	by, blocked = locks.Blocking("staging", "blog", "login")
	testutil.True(t, "a login is blocked by a restore", blocked && by == "restore")

	_, err = locks.TryLock("staging", "blog", "login")
	testutil.ErrorIs(t, err, ops.ErrBusy)

	restore()

	_, ok := locks.Running("staging", "blog")
	testutil.True(t, "released", !ok)
}
