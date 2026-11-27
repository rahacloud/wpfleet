package ops

import (
	"errors"
	"fmt"
	"slices"
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

// holder is one operation holding a site; a pointer, so a release removes
// exactly its own entry when two operations of the same name hold the site.
type holder struct {
	action string
}

// Locks lets one operation run per site and environment at a time, except
// those Beside lets run together. The zero value is ready to use and lets
// nothing run beside anything; a Locks must not be copied after first use.
// The locks live in memory, so they hold within one process only.
type Locks struct {
	// Beside reports whether the operation wanted may start while running
	// holds the site. It is asked about every operation holding the site and
	// should answer the same both ways round; nil allows nothing.
	Beside func(running, wanted string) bool

	mu   sync.Mutex
	held map[lockKey][]*holder
}

// TryLock takes the lock of site in env for action, without waiting. It
// returns the function that releases it, which may be called more than once.
// When an operation that Beside does not allow beside action holds the site
// it returns a *BusyError naming that operation.
func (l *Locks) TryLock(env, site, action string) (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := lockKey{env: env, site: site}

	if running, ok := l.blocking(key, action); ok {
		return nil, &BusyError{Env: env, Site: site, Action: running}
	}

	if l.held == nil {
		l.held = map[lockKey][]*holder{}
	}

	h := &holder{action: action}
	l.held[key] = append(l.held[key], h)

	var once sync.Once

	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()

			l.held[key] = slices.DeleteFunc(l.held[key], func(other *holder) bool { return other == h })
			if len(l.held[key]) == 0 {
				delete(l.held, key)
			}
		})
	}, nil
}

// Running returns the operation that holds site in env, the one that took
// it first when several do, if any.
func (l *Locks) Running(env, site string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	holders := l.held[lockKey{env: env, site: site}]
	if len(holders) == 0 {
		return "", false
	}

	return holders[0].action, true
}

// Blocking returns the operation holding site in env that would make
// TryLock refuse action now, if any, so a page can say why a button would
// be refused before anyone presses it.
func (l *Locks) Blocking(env, site, action string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.blocking(lockKey{env: env, site: site}, action)
}

// blocking is Blocking with l.mu held.
func (l *Locks) blocking(key lockKey, action string) (string, bool) {
	for _, h := range l.held[key] {
		if l.Beside == nil || !l.Beside(h.action, action) {
			return h.action, true
		}
	}

	return "", false
}
