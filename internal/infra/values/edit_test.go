package values_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// commented is a site file the way people write them: comments everywhere,
// odd spacing, quoting, and a block scalar that must come through untouched.
const commented = `# blog in staging.
site: blog   # the release name

# Built by wpfleet build.
image:
    # the registry path
    repository:   registry.example.com/sites/blog    # keep this comment
    tag: "20261101-090000"
    pullPolicy: IfNotPresent

database:
  password: abcdefghijklmnop1234   # a secret, never printed

wordpress:
  htaccess: |
    # BEGIN WordPress
    <IfModule mod_rewrite.c>
    RewriteEngine On
    RewriteRule ^index\.php$ - [L]
    #   image: repository: tag: these words must not confuse the editor
    </IfModule>
    # END WordPress
salts: {authKey: 'x', secureAuthKey: "y"}
# trailing comment
`

func TestSetImageKeepsEveryOtherByte(t *testing.T) {
	t.Parallel()

	const (
		oldRepo = "repository:   registry.example.com/sites/blog    # keep"
		newRepo = "repository:   registry.example.org/other/blog    # keep"
	)

	path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", commented)

	testutil.NoError(t, values.SetImage(path, "registry.example.com/sites/blog", "20261126-101500"))

	want := strings.Replace(commented, `tag: "20261101-090000"`, `tag: "20261126-101500"`, 1)
	testutil.Equal(t, "file", testutil.ReadFile(t, path), want)

	testutil.NoError(t, values.SetImage(path, "registry.example.org/other/blog", "1.0"))

	want = strings.Replace(want, oldRepo, newRepo, 1)
	want = strings.Replace(want, `tag: "20261126-101500"`, `tag: "1.0"`, 1)
	testutil.Equal(t, "file", testutil.ReadFile(t, path), want)

	v, err := values.Load(path)
	testutil.NoError(t, err)
	testutil.Equal(t, "tag", v.String("image.tag"), "1.0")
	testutil.Contains(t, "htaccess", v.String("wordpress.htaccess"), "RewriteRule")
}

func TestSetImageLayouts(t *testing.T) {
	t.Parallel()

	const two = "two"

	tests := []struct {
		name, tag, in, want string
	}{
		{
			"plain stays plain", two,
			"image:\n  repository: a/blog\n  tag: one\n",
			"image:\n  repository: r/blog\n  tag: two\n",
		},
		{
			"plain that would not be a string is quoted", "2.0",
			"image:\n  repository: a/blog\n  tag: one # c\n",
			"image:\n  repository: r/blog\n  tag: \"2.0\" # c\n",
		},
		{
			"single quoted stays single quoted", "it's",
			"image: \n  repository: 'a/blog'\n  tag: 'one'\n",
			"image: \n  repository: 'r/blog'\n  tag: 'it''s'\n",
		},
		{
			"flow mapping", two,
			"site: blog\nimage: {repository: a/blog, tag: one}\n",
			"site: blog\nimage: {repository: r/blog, tag: two}\n",
		},
		{
			"missing tag is added with the block's indent", two,
			"image:\n    repository: a/blog\n    pullPolicy: Always\n",
			"image:\n    tag: two\n    repository: r/blog\n    pullPolicy: Always\n",
		},
		{
			"empty image key", two,
			"site: blog\nimage:\n# end\n",
			"site: blog\nimage:\n  repository: r/blog\n  tag: two\n# end\n",
		},
		{
			"missing image block is appended", two,
			"site: blog\nresources:\n    requests: {}\n# end",
			"site: blog\nresources:\n    requests: {}\n# end\nimage:\n    repository: r/blog\n    tag: two\n",
		},
		{"empty file", two, "", "image:\n  repository: r/blog\n  tag: two\n"},
		{"unchanged", "one", "image:\n  repository: r/blog\n  tag: one\n", "image:\n  repository: r/blog\n  tag: one\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", tt.in)

			testutil.NoError(t, values.SetImage(path, "r/blog", tt.tag))
			testutil.Equal(t, "file", testutil.ReadFile(t, path), tt.want)
		})
	}
}

func TestSetImageRefuses(t *testing.T) {
	t.Parallel()

	for name, in := range map[string]string{
		"block scalar tag": "image:\n  repository: a\n  tag: |\n    one\n",
		"anchor":           "image:\n  repository: &r a\n  tag: one\n",
		"flow without tag": "image: {repository: a}\n",
		"image is a list":  "image: [a]\n",
		"top-level list":   "- a\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", in)

			testutil.ErrorIs(t, values.SetImage(path, "r", "t"), values.ErrUnsupportedLayout)
			testutil.Equal(t, "a refused edit leaves the file alone", testutil.ReadFile(t, path), in)
		})
	}

	testutil.ErrorIs(t, values.SetImage(filepath.Join(t.TempDir(), "missing.yaml"), "r", "t"), os.ErrNotExist)

	path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", "a: 1\n")
	testutil.ErrorIs(t, values.SetImage(path, "", "t"), values.ErrEmptyImage)
	testutil.Error(t, values.SetImage(testutil.WriteFile(t, t.TempDir(), "bad.yaml", "a: ["), "r", "t"), "bad YAML")
}

func TestSetImageKeepsMode(t *testing.T) {
	t.Parallel()

	const mode = 0o640

	path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", "image:\n  repository: a\n  tag: one\n")
	testutil.NoError(t, os.Chmod(path, mode))

	testutil.NoError(t, values.SetImage(path, "b", "two"))

	info, err := os.Stat(path)
	testutil.NoError(t, err)
	testutil.Equal(t, "mode", info.Mode().Perm(), os.FileMode(mode))

	entries, err := os.ReadDir(filepath.Dir(path))
	testutil.NoError(t, err)
	testutil.Equal(t, "files left in the directory", len(entries), 1)
}

func TestPlanImageWritesNothing(t *testing.T) {
	t.Parallel()

	path := testutil.WriteFile(t, t.TempDir(), "blog.yaml", "site: blog\n")

	e, err := values.PlanImage(path, "r/blog", "v1")
	testutil.NoError(t, err)
	testutil.True(t, "changed", e.Changed())
	testutil.Equal(t, "not written yet", testutil.ReadFile(t, path), "site: blog\n")

	testutil.NoError(t, e.Write())
	testutil.Equal(t, "written", testutil.ReadFile(t, path), "site: blog\nimage:\n  repository: r/blog\n  tag: v1\n")

	// A file that changed after the plan is not overwritten.
	e, err = values.PlanImage(path, "r/blog", "v2")
	testutil.NoError(t, err)
	testutil.WriteFile(t, filepath.Dir(path), "blog.yaml", "site: blog\n# edited\n")
	testutil.ErrorIs(t, e.Write(), values.ErrChangedOnDisk)

	testutil.WriteFile(t, filepath.Dir(path), "blog.yaml", "site: blog\nimage:\n  repository: r/blog\n  tag: v1\n")

	same, err := values.PlanImage(path, "r/blog", "v1")
	testutil.NoError(t, err)
	testutil.True(t, "the same image changes nothing", !same.Changed())
	testutil.NoError(t, same.Write())
}
