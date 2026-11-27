package ops

import (
	"errors"
	"fmt"
	"sync"
)

// ErrBusy is wrapped by the error of TryLock when another operation holds
// the site.
var ErrBusy = errors.New("another operation is running on the site")

// BusyError is the error of TryLock on a site that is busy. It wraps ErrBusy.
type BusyError struct {
	// Env and Site are the site that is busy.
	Env, Site string
	// Action is the operation already running on it.
	Action string
}

// Error says which operation holds the site.
func (e *BusyError) Error() string {
	return fmt.Sprintf("%s in %s: %s is running", e.Site, e.Env, e.Action)
}

// Unwrap gives ErrBusy.
func (e *BusyError) Unwrap() error {
	return ErrBusy
}

// lockKey is one site in one environment.
type lockKey struct {
	env, site string
}

// Locks lets one operation run per site and environment at a time. The zero
// value is ready to use; a Locks must not be copied after first use. The
// locks live in memory, so they hold within one process only.
type Locks struct {
	mu   sync.Mutex
	held map[lockKey]string
}

// TryLock takes the lock of site in env for action, without waiting. It
// returns the function that releases it, which may be called more than once.
// When another operation holds the site it returns a *BusyError naming that
// operation.
func (l *Locks) TryLock(env, site, action string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := lockKey{env: env, site: site}

	if running, ok := l.held[key]; ok {
		return nil, &BusyError{Env: env, Site: site, Action: running}
	}

	if l.held == nil {
		l.held = map[lockKey]string{}
	}

	l.held[key] = action

	var once sync.Once

	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			delete(l.held, key)
		})
	}, nil
}

// Running returns the operation that holds site in env, if any.
func (l *Locks) Running(env, site string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	action, ok := l.held[lockKey{env: env, site: site}]

	return action, ok
}
