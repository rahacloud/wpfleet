// Package opstest holds fakes of what the operations reach outside the
// process, for the tests of internal/ops and of its callers.
package opstest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rahacloud/wpfleet/internal/ops"
)

// ErrWP is what the fake wp-cli fails with.
var ErrWP = errors.New("wp-cli failed")

// ClockKey is the call that reads the site's clock.
const ClockKey = "eval echo time();"

// MultisiteKey is the call that asks whether the site is a multisite.
const MultisiteKey = "eval echo is_multisite() ? 'yes' : 'no';"

// keyArgs is how many arguments key the fake's answers.
const keyArgs = 2

// SiteClock is the site's time in the fake: 1_800_000_000.
const SiteClock = "1800000000"

// WP is a wp-cli that records its calls and answers them from a table keyed
// by the first two arguments joined with a space.
type WP struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]string
	fail    string
}

// NewWP returns a fake wp-cli that answers from answers.
func NewWP(answers map[string]string) *WP {
	return &WP{mu: sync.Mutex{}, calls: nil, answers: answers, fail: ""}
}

// FailOn makes the call with the given key fail with ErrWP.
func (f *WP) FailOn(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fail = key
}

// Output answers args; the call named by FailOn fails.
func (f *WP) Output(_ context.Context, pod string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, append([]string{pod}, args...))

	key := strings.Join(args[:min(keyArgs, len(args))], " ")
	if key == f.fail {
		return "", ErrWP
	}

	return f.answers[key], nil
}

// Calls returns every call so far, each the pod followed by the arguments.
func (f *WP) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

// Call returns the first call whose arguments start with prefix, the pod
// first, and fails the test when there is none.
func (f *WP) Call(tb testing.TB, prefix ...string) []string {
	tb.Helper()

	for _, c := range f.Calls() {
		if len(c) > len(prefix) && slices.Equal(c[1:len(prefix)+1], prefix) {
			return c
		}
	}

	tb.Fatalf("no wp %s in %v", strings.Join(prefix, " "), f.Calls())

	return nil
}

// Stored decodes the option that the fake was asked to add, and returns its
// name too.
func (f *WP) Stored(tb testing.TB) (string, ops.StoredLogin) {
	tb.Helper()

	c := f.Call(tb, "option", "add")

	var s ops.StoredLogin

	err := json.Unmarshal([]byte(c[4]), &s)
	if err != nil {
		tb.Fatalf("decoding the stored login: %v", err)
	}

	return c[3], s
}

// SingleSite answers like a site that is not a multisite, whose first
// administrator is 7, whose user lookups give 12, and whose clock reads
// SiteClock.
func SingleSite() map[string]string {
	return map[string]string{MultisiteKey: "no", "user list": "7", "user get": "12", ClockKey: SiteClock}
}

// Seed is randomness that makes the same login token every time.
func Seed() *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0xfb}, ops.LoginTokenBytes))
}
