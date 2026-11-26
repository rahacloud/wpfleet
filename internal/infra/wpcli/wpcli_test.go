package wpcli_test

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	phar     = "<?php // not really wp-cli"
	pharPath = "/download/wp-cli-2.12.0.phar"
	pod      = "blog-1"
)

func pharServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pharPath {
			http.NotFound(w, r)

			return
		}

		hits.Add(1)

		_, _ = io.WriteString(w, phar)
	}))
	t.Cleanup(srv.Close)

	return srv, &hits
}

func sum(s string) string {
	h := sha512.Sum512([]byte(s))

	return hex.EncodeToString(h[:])
}

func TestEnsureInCaches(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	ctx := t.Context()
	url := srv.URL + pharPath

	local, err := wpcli.EnsureIn(ctx, srv.Client(), dir, url, strings.ToUpper(sum(phar)))
	testutil.NoError(t, err)
	testutil.Equal(t, "cached phar", testutil.ReadFile(t, local), phar)
	testutil.True(t, "named as in the URL", strings.HasSuffix(local, "wp-cli-2.12.0.phar"))

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, url, sum(phar))
	testutil.NoError(t, err)

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, url, "")
	testutil.NoError(t, err)
	testutil.Equal(t, "downloads", hits.Load(), int32(1))
}

func TestEnsureInRejects(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	ctx := t.Context()
	url := srv.URL + pharPath

	_, err := wpcli.EnsureIn(ctx, srv.Client(), dir, url, "")
	testutil.NoError(t, err)

	// A cached file that no longer matches is fetched again, and a download
	// that does not match is refused and not kept.
	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, url, sum("other"))
	testutil.ErrorIs(t, err, wpcli.ErrChecksum)
	testutil.Equal(t, "downloads", hits.Load(), int32(2))

	entries, err := os.ReadDir(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, "files left in the cache", len(entries), 0)

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, srv.URL+"/missing.phar", "")
	testutil.Error(t, err, "a 404")

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, "http://[::1", "")
	testutil.Error(t, err, "a bad URL")
}

func TestEnsureUsesUserCache(t *testing.T) {
	srv, _ := pharServer(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	local, err := wpcli.Ensure(t.Context(), srv.URL+pharPath, "")
	testutil.NoError(t, err)
	testutil.True(t, "under the user cache", strings.HasPrefix(local, cache))
	testutil.Contains(t, "path", local, "wpfleet")
}

// fakeWP answers wp-cli calls with a URL, and "fail" with an error.
func fakeWP(call kubetest.Call, stdout, stderr io.Writer) error {
	if slices.Contains(call.Command, "fail") {
		_, _ = io.WriteString(stderr, "Error: no such command\n")

		return kubetest.Exit(1)
	}

	_, err := io.WriteString(stdout, "https://blog.example.com\n")
	if err != nil {
		return fmt.Errorf("writing stdout: %w", err)
	}

	return nil
}

func TestRunner(t *testing.T) {
	t.Parallel()

	local := testutil.WriteFile(t, t.TempDir(), "wp-cli.phar", phar)

	k := kubetest.New("wp-staging")
	k.Handle(fakeWP)

	r := wpcli.NewRunner(k, local)
	ctx := t.Context()

	out, err := r.Output(ctx, pod, "option", "get", "home")
	testutil.NoError(t, err)
	testutil.Equal(t, "output", out, "https://blog.example.com")

	_, err = r.Output(ctx, pod, "option", "get", "siteurl")
	testutil.NoError(t, err)

	data, ok := k.File(pod, wpcli.RemotePath)
	testutil.True(t, "phar copied", ok)
	testutil.Equal(t, "phar in pod", string(data), phar)

	// A second runner finds the phar the first one copied.
	_, err = wpcli.NewRunner(k, local).Output(ctx, pod, "core", "version")
	testutil.NoError(t, err)

	_, err = r.Output(ctx, pod, "fail")
	testutil.Error(t, err, "failing command")
	testutil.Contains(t, "error", err.Error(), "no such command")

	counts := map[string]int{}

	for _, c := range k.Calls() {
		counts[c.Command[0]]++

		if c.Command[0] == "php" {
			testutil.DeepEqual(t, "wp-cli command", c.Command[:4], wpcli.Command())
			testutil.Equal(t, "container", c.Container, kube.WordPressContainer)
		}
	}

	testutil.Equal(t, "copies", counts["tar"], 1)
	testutil.Equal(t, "checks", counts["test"], 2)
	testutil.DeepEqual(t, "Command", wpcli.Command("cli", "version"),
		[]string{"php", "/tmp/wp-cli.phar", "--allow-root", "--path=/var/www/html", "cli", "version"})
}
