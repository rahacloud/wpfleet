package wpcli_test

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func src(url, sha string) wpcli.Source {
	return wpcli.Source{URL: url, SHA512: sha, AllowUnverified: false}
}

func unverified(url string) wpcli.Source {
	return wpcli.Source{URL: url, SHA512: "", AllowUnverified: true}
}

func TestEnsureInCaches(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	ctx := t.Context()
	url := srv.URL + pharPath

	local, err := wpcli.EnsureIn(ctx, srv.Client(), dir, src(url, strings.ToUpper(sum(phar))))
	testutil.NoError(t, err)
	testutil.Equal(t, "cached phar", testutil.ReadFile(t, local), phar)
	testutil.Equal(t, "named by the sum", filepath.Base(local), "wp-cli-"+sum(phar)[:32]+".phar")

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, src(url, sum(phar)))
	testutil.NoError(t, err)
	testutil.Equal(t, "downloads", hits.Load(), int32(1))
}

func TestEnsureInRefusesNoSum(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	url := srv.URL + pharPath

	_, err := wpcli.EnsureIn(t.Context(), srv.Client(), dir, src(url, ""))
	testutil.ErrorIs(t, err, wpcli.ErrNoChecksum)
	testutil.Equal(t, "downloads", hits.Load(), int32(0))

	local, err := wpcli.EnsureIn(t.Context(), srv.Client(), dir, unverified(url))
	testutil.NoError(t, err)
	testutil.Equal(t, "unverified phar", testutil.ReadFile(t, local), phar)
}

func TestEnsureInKeysTheCache(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	ctx := t.Context()

	// A file cached for another URL with the same file name is never reused.
	testutil.WriteFile(t, dir, "wp-cli-2.12.0.phar", "stale")

	a, err := wpcli.EnsureIn(ctx, srv.Client(), dir, unverified(srv.URL+pharPath))
	testutil.NoError(t, err)

	b, err := wpcli.EnsureIn(ctx, srv.Client(), dir, unverified(srv.URL+"/mirror"+pharPath))
	testutil.Error(t, err, "the mirror path is a 404, so it was really fetched")
	testutil.Equal(t, "no entry for the failed URL", b, "")
	testutil.Equal(t, "content", testutil.ReadFile(t, a), phar)
	testutil.Equal(t, "downloads", hits.Load(), int32(1))
}

func TestEnsureInRejects(t *testing.T) {
	t.Parallel()

	srv, hits := pharServer(t)
	dir := t.TempDir()
	ctx := t.Context()
	url := srv.URL + pharPath

	// A download that does not match is refused and not kept.
	_, err := wpcli.EnsureIn(ctx, srv.Client(), dir, src(url, sum("other")))
	testutil.ErrorIs(t, err, wpcli.ErrChecksum)
	testutil.Equal(t, "downloads", hits.Load(), int32(1))

	entries, err := os.ReadDir(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, "files left in the cache", len(entries), 0)

	// A cached file that no longer matches its sum is fetched again.
	testutil.WriteFile(t, dir, "wp-cli-"+sum(phar)[:32]+".phar", "truncated")

	local, err := wpcli.EnsureIn(ctx, srv.Client(), dir, src(url, sum(phar)))
	testutil.NoError(t, err)
	testutil.Equal(t, "refetched", testutil.ReadFile(t, local), phar)

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, unverified(srv.URL+"/missing.phar"))
	testutil.Error(t, err, "a 404")

	_, err = wpcli.EnsureIn(ctx, srv.Client(), dir, unverified("http://[::1"))
	testutil.Error(t, err, "a bad URL")
}

func TestEnsureUsesUserCache(t *testing.T) {
	srv, _ := pharServer(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	local, err := wpcli.Ensure(t.Context(), src(srv.URL+pharPath, sum(phar)))
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

	remote := wpcli.RemotePath(sum(phar))
	testutil.Equal(t, "remote named by the sum", remote, "/tmp/wp-cli-"+sum(phar)[:32]+".phar")

	data, ok := k.File(pod, remote)
	testutil.True(t, "phar copied", ok)
	testutil.Equal(t, "phar in pod", string(data), phar)
	testutil.DeepEqual(t, "only the renamed phar is left", k.Files(), []string{pod + ":" + remote})

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
			testutil.DeepEqual(t, "wp-cli command", c.Command[:4], wpcli.Command(remote))
			testutil.Equal(t, "container", c.Container, kube.WordPressContainer)
		}
	}

	testutil.Equal(t, "copies", counts["tar"], 1)
	testutil.Equal(t, "renames", counts["mv"], 1)
	testutil.Equal(t, "checks", counts["test"], 2)
	testutil.DeepEqual(t, "Command", wpcli.Command("/tmp/x.phar", "cli", "version"),
		[]string{"php", "/tmp/x.phar", "--allow-root", "--path=/var/www/html", "cli", "version"})
}

// A phar left by an interrupted copy, or one from another wp-cli, is never
// taken for the local one.
func TestRunnerIgnoresOtherPhars(t *testing.T) {
	t.Parallel()

	local := testutil.WriteFile(t, t.TempDir(), "wp-cli.phar", phar)
	k := kubetest.New("wp-staging")
	k.Handle(fakeWP)

	// Seed the pod with a truncated phar under the old fixed name and under
	// the temporary name pattern, through the fake's own copy.
	partial := testutil.WriteFile(t, t.TempDir(), "partial.phar", "<?php // trunc")
	testutil.NoError(t, k.CopyFile(t.Context(), pod, partial, "/tmp/wp-cli.phar"))
	testutil.NoError(t, k.CopyFile(t.Context(), pod, partial, wpcli.RemotePath(sum(phar))+".0011.part"))

	_, err := wpcli.NewRunner(k, local).Output(t.Context(), pod, "core", "version")
	testutil.NoError(t, err)

	data, ok := k.File(pod, wpcli.RemotePath(sum(phar)))
	testutil.True(t, "phar copied", ok)
	testutil.Equal(t, "the local phar", string(data), phar)
}

// The probe and the commands always attach a stream: the kubelet refuses an
// exec with none, and kubetest refuses it the same way.
func TestRunnerAttachesStreams(t *testing.T) {
	t.Parallel()

	local := testutil.WriteFile(t, t.TempDir(), "wp-cli.phar", phar)
	k := kubetest.New("wp-staging")
	k.Handle(fakeWP)

	err := wpcli.NewRunner(k, local).Run(t.Context(), pod, io.Discard, nil, "core", "version")
	testutil.NoError(t, err)

	err = k.Exec(t.Context(), pod, kube.WordPressContainer, []string{"true"}, nil, nil, nil)
	testutil.ErrorIs(t, err, kube.ErrNoStreams)
}
