package docker_test

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

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/docker"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Names the tests use more than once.
const (
	indexPHP = "index.php"
	cmdBuild = "build"
)

// latin1Name is a file name in ISO-8859-1, "café.php", which is not valid
// UTF-8.
const latin1Name = "caf\xe9.php"

// entry is one tar entry as the tests compare it.
type entry struct {
	typeflag byte
	mode     int64
	link     string
	body     string
}

// sourceTree writes a WordPress-like tree with every kind of path the build
// must keep or leave out.
func sourceTree(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range map[string]string{
		indexPHP:                                  "<?php // front\n",
		"wp-config.php":                           "<?php define('DB_PASSWORD', 'secret');\n",
		"wp-config-sample.php":                    "<?php // sample\n",
		"error_log":                               "PHP Warning\n",
		"wp-admin/error_log":                      "PHP Warning\n",
		"wp-admin/admin.php":                      "<?php // admin\n",
		"wp-content/uploads/2026/11/a.jpg":        "jpeg",
		"wp-content/cache/page.html":              "cached",
		"wp-content/upgrade/tmp.zip":              "zip",
		"wp-content/themes/shop/style.css":        "body{}",
		"wp-content/themes/shop/uploads/keep.txt": "a theme folder named uploads stays",
		"wp-content/themes/shop/" + latin1Name:    "<?php // latin1\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	//nolint:gosec // the test checks that an executable mode survives
	testutil.NoError(t, os.Chmod(filepath.Join(dir, indexPHP), 0o755))
	//nolint:gosec // the test checks that a directory's mode survives
	testutil.NoError(t, os.Chmod(filepath.Join(dir, "wp-admin"), 0o711))
	testutil.NoError(t, os.Symlink(indexPHP, filepath.Join(dir, "home.php")))

	return dir
}

// spec is a valid build of the tree in dir.
func spec(dir string) docker.Spec {
	return docker.Spec{
		Site:    "shop",
		Source:  dir,
		Base:    "docker.io/library/wordpress:7.0.2-php8.4-apache",
		Image:   fleet.ImageRef{Repository: "registry.example.com/sites/shop", Tag: "20261126-101500", Digest: ""},
		Created: time.Date(2026, 11, 26, 10, 15, 0, 0, time.UTC),
	}
}

// readTar reads a tar stream into its entries by name.
func readTar(t *testing.T, r io.Reader) map[string]entry {
	t.Helper()

	entries := map[string]entry{}
	tr := tar.NewReader(r)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}

		testutil.NoError(t, err)

		body, err := io.ReadAll(tr)
		testutil.NoError(t, err)

		testutil.Equal(t, hdr.Name+" uid", hdr.Uid, 0)
		testutil.Equal(t, hdr.Name+" uname", hdr.Uname, "")

		entries[hdr.Name] = entry{typeflag: hdr.Typeflag, mode: hdr.Mode, link: hdr.Linkname, body: string(body)}
	}
}

func TestWriteContext(t *testing.T) {
	t.Parallel()

	dir := sourceTree(t)

	var buf bytes.Buffer

	testutil.NoError(t, docker.WriteContext(&buf, spec(dir)))

	entries := readTar(t, &buf)

	for _, name := range []string{
		"site/wp-config.php",
		"site/error_log",
		"site/wp-admin/error_log",
		"site/wp-content/uploads/",
		"site/wp-content/uploads/2026/11/a.jpg",
		"site/wp-content/cache/page.html",
		"site/wp-content/upgrade/",
	} {
		_, ok := entries[name]
		testutil.True(t, name+" is left out", !ok)
	}

	for _, name := range []string{
		"Dockerfile",
		"site/",
		"site/wp-config-sample.php",
		"site/wp-content/",
		"site/wp-content/themes/shop/style.css",
		"site/wp-content/themes/shop/uploads/keep.txt",
	} {
		_, ok := entries[name]
		testutil.True(t, name+" is kept", ok)
	}

	latin1, ok := entries["site/wp-content/themes/shop/"+latin1Name]
	testutil.True(t, "the non-UTF-8 name is kept byte for byte", ok)
	testutil.Equal(t, "non-UTF-8 body", latin1.body, "<?php // latin1\n")

	testutil.Equal(t, indexPHP, entries["site/index.php"],
		entry{typeflag: tar.TypeReg, mode: 0o755, link: "", body: "<?php // front\n"})
	testutil.Equal(t, "admin.php mode", entries["site/wp-admin/admin.php"].mode, 0o600)
	testutil.Equal(t, "wp-admin", entries["site/wp-admin/"], entry{typeflag: tar.TypeDir, mode: 0o711, link: "", body: ""})
	testutil.Equal(t, "symlink", entries["site/home.php"],
		entry{typeflag: tar.TypeSymlink, mode: 0o777, link: indexPHP, body: ""})

	testutil.Contains(t, "Dockerfile", entries["Dockerfile"].body,
		"FROM docker.io/library/wordpress:7.0.2-php8.4-apache\n",
		"COPY --chown=www-data:www-data site/ /var/www/html/\n",
		`org.opencontainers.image.title="shop"`,
		`org.opencontainers.image.version="20261126-101500"`,
		`org.opencontainers.image.created="2026-11-26T10:15:00Z"`,
	)
}

func TestWriteContextLongName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	long := filepath.Join("wp-content", "plugins", string(bytes.Repeat([]byte("a"), 90)), latin1Name)
	testutil.WriteFile(t, dir, long, "long")

	var buf bytes.Buffer

	testutil.NoError(t, docker.WriteContext(&buf, spec(dir)))
	testutil.Equal(t, "long name", readTar(t, &buf)["site/"+filepath.ToSlash(long)].body, "long")
}

func TestWriteContextMissingSource(t *testing.T) {
	t.Parallel()

	s := spec(filepath.Join(t.TempDir(), "missing"))

	testutil.ErrorIs(t, docker.WriteContext(io.Discard, s), os.ErrNotExist)
}

func TestExcluded(t *testing.T) {
	t.Parallel()

	for rel, want := range map[string]bool{
		"wp-config.php":                  true,
		"wp-content/uploads":             true,
		"wp-content/cache":               true,
		"wp-content/upgrade":             true,
		"error_log":                      true,
		"wp-content/plugins/x/error_log": true,
		"sub/wp-config.php":              false,
		"wp-content/uploads.php":         false,
		"wp-content/themes/t/uploads":    false,
		"error_log.txt":                  false,
		"index.php":                      false,
		// Copies of the configuration and other files with secrets.
		"wp-config.php.bak":                    true,
		"wp-config.php~":                       true,
		"wp-config-old.php":                    true,
		"WP-CONFIG.PHP.save":                   true,
		"wp-config-sample.php":                 false,
		".git":                                 true,
		"wp-content/plugins/p/.git":            true,
		".svn":                                 true,
		".env":                                 true,
		".env.production":                      true,
		"wp-content/themes/t/.env":             true,
		".environment":                         false,
		"backup.sql":                           true,
		"db-2026.SQL.GZ":                       true,
		"wp-content/plugins/p/schema.sql":      false,
		"wp-content/plugins/p/wp-config-x.php": false,
	} {
		testutil.Equal(t, rel, docker.Excluded(rel), want)
	}
}

func TestTags(t *testing.T) {
	t.Parallel()

	plusOne := time.FixedZone("plus-one", 3600)
	testutil.Equal(t, "default", docker.DefaultTag(time.Date(2026, 1, 2, 3, 4, 5, 0, plusOne)), "20260102-020405")

	for _, tag := range []string{"20261126-101500", "v1.2.3", "_x"} {
		testutil.NoError(t, docker.ValidateTag(tag))
	}

	for _, tag := range []string{"", ".x", "-x", "a:b", "a/b", string(bytes.Repeat([]byte("a"), 129))} {
		testutil.ErrorIs(t, docker.ValidateTag(tag), docker.ErrInvalidTag)
	}
}

// fakeRunner records docker invocations and reads a build's context.
type fakeRunner struct {
	calls   [][]string
	context map[string]entry
	fail    string
	readErr bool
}

var errDocker = errors.New("docker failed")

func (f *fakeRunner) Run(_ context.Context, stdin io.Reader, stdout, _ io.Writer, args ...string) error {
	f.calls = append(f.calls, args)

	if args[0] == cmdBuild && !f.readErr {
		var buf bytes.Buffer

		_, err := io.Copy(&buf, stdin)
		if err != nil {
			return fmt.Errorf("reading context: %w", err)
		}

		f.context = map[string]entry{}

		tr := tar.NewReader(&buf)
		for hdr, err := tr.Next(); err == nil; hdr, err = tr.Next() {
			f.context[hdr.Name] = entry{typeflag: hdr.Typeflag, mode: 0, link: "", body: ""}
		}
	}

	_, _ = fmt.Fprintln(stdout, "docker", args[0])

	if args[0] == f.fail {
		return errDocker
	}

	return nil
}

func TestBuildAndPush(t *testing.T) {
	t.Parallel()

	dir := sourceTree(t)
	runner := &fakeRunner{calls: nil, context: nil, fail: "", readErr: false}

	var log bytes.Buffer

	client := docker.New(runner, &log)
	s := spec(dir)

	testutil.NoError(t, client.Build(t.Context(), s))
	testutil.NoError(t, client.Push(t.Context(), s.Image))

	testutil.DeepEqual(t, "calls", runner.calls, [][]string{
		{"build", "--tag", "registry.example.com/sites/shop:20261126-101500", "-"},
		{"push", "registry.example.com/sites/shop:20261126-101500"},
	})

	_, ok := runner.context["site/index.php"]
	testutil.True(t, "docker read the context", ok)
	testutil.Equal(t, "log", log.String(), "docker build\ndocker push\n")
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()

	dir := sourceTree(t)

	runner := &fakeRunner{calls: nil, context: nil, fail: cmdBuild, readErr: true}
	err := docker.New(runner, io.Discard).Build(t.Context(), spec(dir))
	testutil.ErrorIs(t, err, errDocker)

	runner = &fakeRunner{calls: nil, context: nil, fail: "", readErr: false}
	s := spec(filepath.Join(dir, "missing"))
	err = docker.New(runner, io.Discard).Build(t.Context(), s)
	testutil.ErrorIs(t, err, os.ErrNotExist)

	bad := spec(dir)
	bad.Base = "wordpress\nRUN rm -rf /"
	testutil.ErrorIs(t, docker.New(runner, io.Discard).Build(t.Context(), bad), fleet.ErrInvalidImage)

	bad = spec(dir)
	bad.Image.Tag = ""
	testutil.ErrorIs(t, docker.New(runner, io.Discard).Build(t.Context(), bad), docker.ErrInvalidSpec)

	testutil.Equal(t, "invalid specs never reach docker", len(runner.calls), 1)
}

func TestCLIRun(t *testing.T) {
	t.Parallel()

	// sh stands in for docker: the CLI runner only executes a binary.
	var out bytes.Buffer

	cli := docker.CLI{Binary: "sh"}
	testutil.NoError(t, cli.Run(t.Context(), bytes.NewBufferString("echo hello"), &out, io.Discard, "-s"))
	testutil.Equal(t, "stdout", out.String(), "hello\n")

	err := cli.Run(t.Context(), nil, io.Discard, io.Discard, "-c", "exit 3")
	testutil.Error(t, err, "a failing command")
	testutil.Contains(t, "error", err.Error(), "sh -c")
}
