package build_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/build"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Strings the tests use more than once.
const (
	cmdBuild   = "build"
	flagSource = "--source"
	shop       = "shop"
	shopFile   = "environments/staging/shop.yaml"
)

// siteFile is a staging site file with comments that --set must keep.
const siteFile = `# The shop.
site: shop
image:
  # Built by wpfleet build.
  repository: "registry.example.com/sites/shop"
  tag: "20261101-000000" # the first build
database:
  password: not-a-real-password
`

// fakeDocker records docker invocations and the names in a build context.
type fakeDocker struct {
	calls [][]string
	names []string
	fail  string
}

var errPush = errors.New("push refused")

func (f *fakeDocker) Run(_ context.Context, stdin io.Reader, _, stderr io.Writer, args ...string) error {
	f.calls = append(f.calls, args)

	if args[0] == cmdBuild {
		tr := tar.NewReader(stdin)
		for hdr, err := tr.Next(); err == nil; hdr, err = tr.Next() {
			f.names = append(f.names, hdr.Name)
		}

		_, _ = io.Copy(io.Discard, stdin)
	}

	_, _ = fmt.Fprintln(stderr, "progress of", args[0])

	if args[0] == f.fail {
		return errPush
	}

	return nil
}

// fixture writes a fleet with one staging site and a source tree, and
// returns the fleet directory and the source directory.
func fixture(t *testing.T, registry string) (string, string) {
	t.Helper()

	dir := t.TempDir()

	testutil.WriteFile(t, dir, "wpfleet.yaml", "registry: "+registry+`
build:
  base: docker.io/library/wordpress:7.0.2-php8.4-apache
environments:
  - name: staging
    namespace: wp-staging
`)
	testutil.WriteFile(t, dir, "environments/staging/_shared.yaml", "domainSuffix: staging.example.com\n")
	testutil.WriteFile(t, dir, shopFile, siteFile)

	src := t.TempDir()
	testutil.WriteFile(t, src, "index.php", "<?php\n")
	testutil.WriteFile(t, src, "wp-config.php", "<?php // secrets\n")

	return dir, src
}

// run runs wpfleet build with a fake docker and a fixed clock, returning
// stdout, stderr and the error.
func run(t *testing.T, d *fakeDocker, args ...string) (string, string, error) {
	t.Helper()

	now := func() time.Time { return time.Date(2026, 11, 26, 10, 15, 0, 0, time.UTC) }

	var stdout, stderr bytes.Buffer

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &stdout,
		ErrWriter:      &stderr,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{build.NewCommand(d, now)},
	}

	err := root.Run(t.Context(), append([]string{"wpfleet"}, args...))

	return stdout.String(), stderr.String(), err
}

func TestBuild(t *testing.T) {
	t.Parallel()

	dir, src := fixture(t, "registry.example.com/sites")
	d := &fakeDocker{calls: nil, names: nil, fail: ""}

	out, errOut, err := run(t, d, "-f", dir, cmdBuild, shop, flagSource, src)
	testutil.NoError(t, err)

	ref := "registry.example.com/sites/shop:20261126-101500"
	testutil.DeepEqual(t, "calls", d.calls, [][]string{{cmdBuild, "--tag", ref, "-"}, {"push", ref}})
	testutil.DeepEqual(t, "context", d.names, []string{"Dockerfile", "site/", "site/index.php"})
	testutil.Contains(t, "stdout", out, shop, ref)
	testutil.Contains(t, "docker output goes to stderr", errOut, "progress of build", "progress of push")
	testutil.Equal(t, "the site file is untouched without --set", testutil.ReadFile(t, filepath.Join(dir, shopFile)),
		siteFile)
}

func TestBuildSet(t *testing.T) {
	t.Parallel()

	dir, src := fixture(t, "registry.example.com/sites")
	d := &fakeDocker{calls: nil, names: nil, fail: ""}

	out, _, err := run(t, d, "-f", dir, "-e", "staging", "-o", "json",
		cmdBuild, shop, flagSource, src, "--tag", "v2", "--set")
	testutil.NoError(t, err)

	file := filepath.Join(dir, shopFile)
	want := `# The shop.
site: shop
image:
  # Built by wpfleet build.
  repository: "registry.example.com/sites/shop"
  tag: "v2" # the first build
database:
  password: not-a-real-password
`
	testutil.Equal(t, "site file", testutil.ReadFile(t, file), want)
	testutil.Contains(t, "json", out, `"image": "registry.example.com/sites/shop:v2"`, `"file": "`+file+`"`)
	testutil.NotContains(t, "no secrets", out, "not-a-real-password")
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()

	dir, src := fixture(t, "registry.example.com/sites")

	for _, tc := range []struct {
		name string
		args []string
		want error
	}{
		{"no site", []string{cmdBuild, flagSource, src}, build.ErrOneSite},
		{"two sites", []string{cmdBuild, shop, "blog", flagSource, src}, build.ErrOneSite},
		{"bad name", []string{cmdBuild, "Shop", flagSource, src}, fleet.ErrInvalidSiteName},
		{"set without env", []string{cmdBuild, shop, flagSource, src, "--set"}, cmdutil.ErrNoEnv},
		{"set unknown site", []string{"-e", "staging", cmdBuild, "blog", flagSource, src, "--set"}, fleet.ErrUnknownSite},
		{"missing source", []string{cmdBuild, shop, flagSource, filepath.Join(src, "missing")}, os.ErrNotExist},
		{"source is a file", []string{cmdBuild, shop, flagSource, filepath.Join(src, "index.php")}, build.ErrNotDirectory},
	} {
		d := &fakeDocker{calls: nil, names: nil, fail: ""}

		_, _, err := run(t, d, append([]string{"-f", dir}, tc.args...)...)
		testutil.ErrorIs(t, err, tc.want)
		testutil.Equal(t, tc.name+": docker is not run", len(d.calls), 0)
	}

	d := &fakeDocker{calls: nil, names: nil, fail: ""}
	_, _, err := run(t, d, "-f", dir, cmdBuild, shop, flagSource, src, "--tag", "bad:tag")
	testutil.Error(t, err, "an invalid tag")

	noRegistry, _ := fixture(t, `""`)
	_, _, err = run(t, d, "-f", noRegistry, cmdBuild, shop, flagSource, src)
	testutil.ErrorIs(t, err, build.ErrNoRegistry)
	testutil.Equal(t, "docker is not run", len(d.calls), 0)
}

func TestBuildPushFails(t *testing.T) {
	t.Parallel()

	dir, src := fixture(t, "registry.example.com/sites")
	d := &fakeDocker{calls: nil, names: nil, fail: "push"}

	_, _, err := run(t, d, "-f", dir, "-e", "staging", cmdBuild, shop, flagSource, src, "--set")
	testutil.ErrorIs(t, err, errPush)
	testutil.Equal(t, "a failed push sets nothing", testutil.ReadFile(t, filepath.Join(dir, shopFile)), siteFile)
}
