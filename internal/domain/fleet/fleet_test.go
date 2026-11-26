package fleet_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	dev     = "development"
	staging = "staging"
	blog    = "blog"
	shop    = "shop"
)

// mapValues is a flat test double for fleet.Values.
type mapValues struct {
	strings map[string]string
	lists   map[string][]string
}

func (m mapValues) String(path string) string { return m.strings[path] }

func (m mapValues) StringSlice(path string) []string { return m.lists[path] }

// vals builds mapValues from string pairs.
func vals(kv ...string) mapValues {
	m := mapValues{strings: map[string]string{}, lists: map[string][]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		m.strings[kv[i]] = kv[i+1]
	}

	return m
}

func env(name, valuesDir, promoteFrom string) fleet.Environment {
	return fleet.Environment{
		Name:        name,
		Namespace:   "wp-" + name,
		Kubeconfig:  "",
		Context:     "",
		ValuesDir:   valuesDir,
		PromoteFrom: promoteFrom,
	}
}

func testFleet() fleet.Fleet {
	return fleet.Fleet{
		Root:     "/fleet",
		Registry: "registry.example.com/sites/",
		Environments: []fleet.Environment{
			env(dev, "environments/development", ""),
			env(staging, "environments/staging", dev),
		},
	}
}

func TestEnvironment(t *testing.T) {
	t.Parallel()

	f := testFleet()

	got, err := f.Environment(staging)
	testutil.NoError(t, err)
	testutil.Equal(t, "namespace", got.Namespace, "wp-staging")

	_, err = f.Environment("production")
	testutil.ErrorIs(t, err, fleet.ErrUnknownEnvironment)
	testutil.Contains(t, "error", err.Error(), "development, staging")

	src, err := f.PromotionSource(staging)
	testutil.NoError(t, err)
	testutil.Equal(t, "promotion source", src.Name, dev)

	_, err = f.PromotionSource(dev)
	testutil.ErrorIs(t, err, fleet.ErrNoPromotionSource)

	_, err = f.PromotionSource("production")
	testutil.ErrorIs(t, err, fleet.ErrUnknownEnvironment)

	testutil.Equal(t, "image", f.Image(blog, "1").String(), "registry.example.com/sites/blog:1")
}

func TestDiscoverSites(t *testing.T) {
	t.Parallel()

	e := testFleet().Environments[0]

	sites, err := fleet.DiscoverSites(e, []string{"shop.yaml", "_shared.yaml", "README.md", "blog.yaml", "notes.yml"})
	testutil.NoError(t, err)

	names := make([]string, 0, len(sites))
	for _, s := range sites {
		names = append(names, s.Name)
	}

	testutil.DeepEqual(t, "names", names, []string{blog, shop})

	want := []string{
		filepath.Join("environments/development", "_shared.yaml"),
		filepath.Join("environments/development", "blog.yaml"),
	}
	testutil.DeepEqual(t, "values files", sites[0].ValuesFiles, want)
	testutil.Equal(t, "site file", sites[0].File(), want[1])
	testutil.Equal(t, "site env", sites[0].Env.Name, dev)

	sites, err = fleet.DiscoverSites(e, []string{"blog.yaml"})
	testutil.NoError(t, err)
	testutil.Equal(t, "files without _shared.yaml", len(sites[0].ValuesFiles), 1)

	_, err = fleet.DiscoverSites(e, []string{"Blog.yaml", "_other.yaml", "ok.yaml"})
	testutil.ErrorIs(t, err, fleet.ErrInvalidSiteName)
	testutil.Contains(t, "error", err.Error(), "Blog", "_other")

	testutil.Equal(t, "File of no files", fleet.Site{Name: blog, Env: e, ValuesFiles: nil}.File(), "")
}

func TestValidateSiteName(t *testing.T) {
	t.Parallel()

	for name, ok := range map[string]bool{
		blog: true, "shop-2": true, "a": true, strings.Repeat("a", 63): true,
		"": false, "-blog": false, "blog-": false, "Blog": false, "blog_x": false, strings.Repeat("a", 64): false,
	} {
		err := fleet.ValidateSiteName(name)
		testutil.Equal(t, "valid "+name, err == nil, ok)
	}
}

func TestSelect(t *testing.T) {
	t.Parallel()

	e := testFleet().Environments[0]
	sites := []fleet.Site{
		{Name: blog, Env: e, ValuesFiles: nil},
		{Name: shop, Env: e, ValuesFiles: nil},
	}

	got, err := fleet.Select(sites, []string{shop, blog})
	testutil.NoError(t, err)
	testutil.Equal(t, "first", got[0].Name, shop)
	testutil.Equal(t, "second", got[1].Name, blog)

	_, err = fleet.Select(sites, []string{"news", blog, "wiki"})
	testutil.ErrorIs(t, err, fleet.ErrUnknownSite)
	testutil.Contains(t, "error", err.Error(), "news, wiki")
}

func TestHost(t *testing.T) {
	t.Parallel()

	const suffix, label = "domainSuffix", "hostLabelSuffix"

	withDomains := vals("site", blog, suffix, "dev.example.com")
	withDomains.lists["domains"] = []string{"www.example.com", "example.com"}

	tests := []struct {
		name    string
		values  mapValues
		want    string
		wantErr error
	}{
		{"first domain wins", withDomains, "www.example.com", nil},
		{"suffix", vals("site", blog, suffix, "dev.example.com"), "blog.dev.example.com", nil},
		{"label suffix", vals("site", blog, suffix, "example.com", label, "-dev"), "blog-dev.example.com", nil},
		{"neither", vals("site", blog), "", fleet.ErrNoHost},
		{"bad label suffix", vals("site", blog, suffix, "example.com", label, ".dev"), "", fleet.ErrInvalidValue},
		{"bad site", vals("site", "Blog", suffix, "example.com"), "", fleet.ErrInvalidSiteName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := fleet.Host(tt.values)
			testutil.Equal(t, "host", got, tt.want)

			if tt.wantErr != nil {
				testutil.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestPlanPromotion(t *testing.T) {
	t.Parallel()

	const repo = "registry.example.com/sites/blog"

	src := vals("image.repository", repo, "image.tag", "2")
	dst := vals("image.repository", repo, "image.tag", "1")

	plan, err := fleet.PlanPromotion(blog, dev, staging, src, dst)
	testutil.NoError(t, err)
	testutil.True(t, "changed", plan.Changed())
	testutil.Equal(t, "target tag", plan.Target.Tag, "2")
	testutil.Equal(t, "current tag", plan.Current.Tag, "1")

	plan, err = fleet.PlanPromotion(blog, dev, staging, src, src)
	testutil.NoError(t, err)
	testutil.True(t, "unchanged", !plan.Changed())

	_, err = fleet.PlanPromotion(blog, dev, staging, vals(), dst)
	testutil.ErrorIs(t, err, fleet.ErrNoImage)
}
