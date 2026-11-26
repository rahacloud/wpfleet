package values_test

import (
	"path/filepath"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const sharedYAML = `domainSuffix: dev.example.com
domains: [a.example.com, b.example.com]
image:
  repository: docker.io/library/wordpress
  tag: "7"
  pullPolicy: IfNotPresent
wordpress:
  debug: true
  accessibleHosts: [pay.example.com]
backup:
  enabled: true
`

const siteYAML = `site: blog
domains: [blog.example.com]
image:
  tag: 20261126
wordpress:
  accessibleHosts: []
backup: null
`

func loadMerged(t *testing.T) values.Values {
	t.Helper()

	dir := t.TempDir()

	v, err := values.Load(
		testutil.WriteFile(t, dir, "_shared.yaml", sharedYAML),
		testutil.WriteFile(t, dir, "blog.yaml", siteYAML),
	)
	testutil.NoError(t, err)

	return v
}

func TestLoadMergesLikeHelm(t *testing.T) {
	t.Parallel()

	v := loadMerged(t)

	for path, want := range map[string]string{
		"site":             "blog",
		"domainSuffix":     "dev.example.com",
		"image.repository": "docker.io/library/wordpress",
		"image.tag":        "20261126",
		"image.pullPolicy": "IfNotPresent",
		"missing.key":      "",
		"image":            "",
	} {
		testutil.Equal(t, "String("+path+")", v.String(path), want)
	}

	testutil.DeepEqual(t, "lists replace", v.StringSlice("domains"), []string{"blog.example.com"})
	testutil.Equal(t, "empty list replaces", len(v.StringSlice("wordpress.accessibleHosts")), 0)
	testutil.True(t, "missing list is nil", v.StringSlice("nothing") == nil)
	testutil.True(t, "Bool", v.Bool("wordpress.debug"))
	testutil.True(t, "missing Bool", !v.Bool("missing"))

	raw, ok := v.Lookup("backup")
	testutil.True(t, "explicit null is kept for Helm", ok && raw == nil)
}

func TestGet(t *testing.T) {
	t.Parallel()

	v := loadMerged(t)

	_, ok := values.Get[string](v, "image.tag")
	testutil.True(t, "Get does not convert an int to a string", !ok)

	n, ok := values.Get[int](v, "image.tag")
	testutil.True(t, "Get[int] finds the tag", ok)
	testutil.Equal(t, "Get[int]", n, 20261126)

	_, ok = values.Get[int](v, "image.tag.deeper")
	testutil.True(t, "a path through a scalar is missing", !ok)

	tree := values.New(values.Tree{"f": 1.5, "b": false, "big": int64(1) << 40, "u": uint64(7)})
	testutil.Equal(t, "float", tree.String("f"), "1.5")
	testutil.Equal(t, "bool", tree.String("b"), "false")
	testutil.Equal(t, "int64", tree.String("big"), "1099511627776")
	testutil.Equal(t, "uint64", tree.String("u"), "7")
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := values.Load(filepath.Join(dir, "missing.yaml"))
	testutil.Error(t, err, "missing file")

	_, err = values.Load(testutil.WriteFile(t, dir, "bad.yaml", "a: ["))
	testutil.Error(t, err, "bad YAML")

	v, err := values.Load(testutil.WriteFile(t, dir, "empty.yaml", ""))
	testutil.NoError(t, err)
	testutil.Equal(t, "empty file", len(v.Tree()), 0)
}

func TestMergeDoesNotModify(t *testing.T) {
	t.Parallel()

	base := values.Tree{"a": values.Tree{"b": 1}}
	over := values.Tree{"a": values.Tree{"c": 2}}

	got := values.Merge(base, over)

	testutil.DeepEqual(t, "base", base, values.Tree{"a": values.Tree{"b": 1}})
	testutil.DeepEqual(t, "merged", got, values.Tree{"a": values.Tree{"b": 1, "c": 2}})
	testutil.Equal(t, "New(nil)", values.New(nil).String("x"), "")
	testutil.Equal(t, "Merge(nil, nil)", len(values.Merge(nil, nil)), 0)
}
