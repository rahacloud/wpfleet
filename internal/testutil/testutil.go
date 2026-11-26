// Package testutil holds the assertions and file helpers that wpfleet's tests
// share. Every helper stops the test on failure.
package testutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet"
)

// NoError stops the test when err is not nil.
func NoError(tb testing.TB, err error) {
	tb.Helper()

	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
}

// Error stops the test when err is nil.
func Error(tb testing.TB, err error, what string) {
	tb.Helper()

	if err == nil {
		tb.Fatalf("%s: want an error, got none", what)
	}
}

// ErrorIs stops the test unless err wraps target.
func ErrorIs(tb testing.TB, err, target error) {
	tb.Helper()

	if !errors.Is(err, target) {
		tb.Fatalf("error = %v, want one that wraps %v", err, target)
	}
}

// Equal stops the test unless got equals want.
func Equal[T comparable](tb testing.TB, what string, got, want T) {
	tb.Helper()

	if got != want {
		tb.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// DeepEqual stops the test unless got and want are deeply equal.
func DeepEqual[T any](tb testing.TB, what string, got, want T) {
	tb.Helper()

	if !reflect.DeepEqual(got, want) {
		tb.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// True stops the test unless cond holds.
func True(tb testing.TB, what string, cond bool) {
	tb.Helper()

	if !cond {
		tb.Fatalf("%s: not true", what)
	}
}

// Contains stops the test unless s contains every one of subs.
func Contains(tb testing.TB, what, s string, subs ...string) {
	tb.Helper()

	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			tb.Fatalf("%s lacks %q:\n%s", what, sub, s)
		}
	}
}

// NotContains stops the test when s contains sub.
func NotContains(tb testing.TB, what, s, sub string) {
	tb.Helper()

	if strings.Contains(s, sub) {
		tb.Fatalf("%s must not contain %q:\n%s", what, sub, s)
	}
}

// Modes of the files and directories WriteFile creates.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// WriteFile writes content to dir/name, creating parent directories, and
// returns the path.
func WriteFile(tb testing.TB, dir, name, content string) string {
	tb.Helper()

	path := filepath.Join(dir, name)

	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		tb.Fatal(err)
	}

	err = os.WriteFile(path, []byte(content), fileMode)
	if err != nil {
		tb.Fatal(err)
	}

	return path
}

// ReadFile returns the contents of path.
func ReadFile(tb testing.TB, path string) string {
	tb.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // tests read the files they wrote
	if err != nil {
		tb.Fatal(err)
	}

	return string(data)
}

// pairLen is the length of an old/new replacement pair.
const pairLen = 2

// CustomChart writes the embedded chart to dir/name, as a fleet with its own
// chart has it, with each old/new pair of replacements applied to its
// values.yaml, and returns its path. A replacement that matches nothing stops
// the test.
func CustomChart(tb testing.TB, dir, name string, replacements ...string) string {
	tb.Helper()

	root, err := fs.Sub(wpfleet.Chart, wpfleet.ChartRoot)
	if err != nil {
		tb.Fatal(err)
	}

	dst := filepath.Join(dir, name)

	err = os.CopyFS(dst, root)
	if err != nil {
		tb.Fatal(err)
	}

	valuesFile := filepath.Join(dst, "values.yaml")
	content := ReadFile(tb, valuesFile)

	for pair := range slices.Chunk(replacements, pairLen) {
		if len(pair) != pairLen || !strings.Contains(content, pair[0]) {
			tb.Fatalf("chart values.yaml: cannot replace %q", pair)
		}

		content = strings.Replace(content, pair[0], pair[1], 1)
	}

	WriteFile(tb, dst, "values.yaml", content)

	return dst
}
