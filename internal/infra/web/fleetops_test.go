package web_test

import (
	"path/filepath"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// fleetDir writes a fleet of development and staging and returns its
// directory.
func fleetDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range map[string]string{
		"wpfleet.yaml": `registry: registry.example.com/sites
environments:
  - name: development
    namespace: wp-development
  - name: staging
    namespace: wp-staging
    promoteFrom: development
`,
		"environments/development/blog.yaml": "site: blog\nimage:\n  repository: r.example.com/blog\n  tag: v4\n",
		"environments/development/shop.yaml": "site: shop\nimage:\n  repository: r.example.com/shop\n  tag: v1\n",
		stagingBlogFile: "# The blog in staging.\nsite: blog\n" +
			"image:\n  repository: r.example.com/blog\n  tag: v3 # pinned\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// fleetOps is FleetOps on dir.
func fleetOps(dir string) web.FleetOps {
	return web.FleetOps{Dir: func() string { return dir }, Getenv: func(string) string { return "" }, Authors: nil}
}

func TestFleetOpsReadsTheFleet(t *testing.T) {
	t.Parallel()

	o := fleetOps(fleetDir(t))

	envs, err := o.Environments()
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "environments", envs, []web.Environment{
		{Name: envDev, PromoteFrom: ""},
		{Name: envStaging, PromoteFrom: envDev},
	})

	sites, err := o.Sites(envDev)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "sites", sites, []string{siteBlog, siteShop})

	_, err = o.Sites("production")
	testutil.Error(t, err, "an unknown environment")

	file, err := o.SiteFile(envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.Equal(t, "site file", file, stagingBlogFile)

	plan, err := o.PlanPromotion(envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.Equal(t, "current", plan.Current.Tag, "v3")
	testutil.Equal(t, "target", plan.Target.Tag, "v4")
	testutil.True(t, "changed", plan.Changed())
}

func TestFleetOpsPromotesInTheGivenDirectory(t *testing.T) {
	t.Parallel()

	served := fleetDir(t)
	clone := fleetDir(t)

	applied, files, err := fleetOps(served).Promote(t.Context(), clone, envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "files", files, []string{stagingBlogFile})
	testutil.Equal(t, "applied", applied.Target.Tag, "v4")
	testutil.Equal(t, "from the clone", applied.Current.Tag, "v3")

	testutil.Contains(t, "the clone's file", testutil.ReadFile(t, filepath.Join(clone, files[0])),
		"# The blog in staging.", "tag: v4 # pinned")
	testutil.Contains(t, "the served file", testutil.ReadFile(t, filepath.Join(served, files[0])), "tag: v3")
}
