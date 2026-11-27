package promote_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/cmd/promote"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// stagingShop is the shop's staging file: the image to promote.
const stagingShop = `site: shop
image:
  repository: r.example.com/shop
  tag: "20261126-101500"
`

// productionShop is the shop's production file, with comments and order
// that a promotion must keep.
const productionShop = `# The shop in production.
site: shop
domains:
  - shop.example.com
image:
  # Promoted from staging.
  repository: r.example.com/shop # pinned
  tag: '20261101-000000'
database:
  password: not-a-real-password
`

// read returns the file at rel in the fleet dir.
func read(t *testing.T, dir, rel string) string {
	t.Helper()

	return testutil.ReadFile(t, filepath.Join(dir, rel))
}

// fixture writes a fleet with development, staging and production, and
// returns its directory.
func fixture(t *testing.T) string {
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
  - name: production
    namespace: wp-production
    promoteFrom: staging
`,
		"environments/development/blog.yaml":   "site: blog\n",
		"environments/staging/_shared.yaml":    "image:\n  repository: docker.io/library/wordpress\n  tag: latest\n",
		"environments/staging/shop.yaml":       stagingShop,
		"environments/staging/blog.yaml":       "site: blog\nimage:\n  repository: r.example.com/blog\n  tag: v3\n",
		"environments/staging/news.yaml":       "site: news\n",
		"environments/production/shop.yaml":    productionShop,
		"environments/production/blog.yaml":    "site: blog\nimage:\n  repository: r.example.com/blog\n  tag: v3\n",
		"environments/production/news.yaml":    "site: news\n",
		"environments/production/_shared.yaml": "domainSuffix: example.com\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// run runs wpfleet promote and returns stdout and the error.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var stdout bytes.Buffer

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &stdout,
		ErrWriter:      &bytes.Buffer{},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{promote.Command()},
	}

	err := root.Run(t.Context(), append([]string{"wpfleet"}, args...))

	return stdout.String(), err
}

func TestPromote(t *testing.T) {
	t.Parallel()

	dir := fixture(t)

	out, err := run(t, "-f", dir, "promote", "shop", "blog", "shop", "--to", "production")
	testutil.NoError(t, err)

	want := `# The shop in production.
site: shop
domains:
  - shop.example.com
image:
  # Promoted from staging.
  repository: r.example.com/shop # pinned
  tag: '20261126-101500'
database:
  password: not-a-real-password
`
	testutil.Equal(t, "production shop", read(t, dir, "environments/production/shop.yaml"), want)
	testutil.Equal(t, "table", out, `SITE  FROM     TO          IMAGE
shop  staging  production  r.example.com/shop:20261101-000000 -> r.example.com/shop:20261126-101500
blog  staging  production  r.example.com/blog:v3 (unchanged)
`)
	testutil.NotContains(t, "no secrets", out, "not-a-real-password")
	testutil.Equal(t, "the source is untouched", read(t, dir, "environments/staging/shop.yaml"), stagingShop)
}

func TestPromoteMissingSiteAndNewImage(t *testing.T) {
	t.Parallel()

	dir := fixture(t)

	out, err := run(t, "-f", dir, "-o", "json", "promote", "shop", "--to", "staging")
	testutil.ErrorIs(t, err, fleet.ErrUnknownSite)
	testutil.Equal(t, "nothing printed", out, "")

	testutil.WriteFile(t, dir, "environments/development/shop.yaml", stagingShop)

	out, err = run(t, "-f", dir, "-o", "json", "promote", "shop", "--to", "staging")
	testutil.NoError(t, err)
	testutil.Contains(t, "json", out, `"site": "shop"`, `"from": "development"`, `"changed": false`)

	testutil.WriteFile(t, dir, "environments/staging/news.yaml",
		"site: news\nimage:\n  repository: r.example.com/news\n  tag: v1\n")

	out, err = run(t, "-f", dir, "-o", "json", "promote", "news", "--to", "production")
	testutil.NoError(t, err)
	testutil.Contains(t, "json", out, `"changed": true`, `"current": {`, `"tag": "v1"`)
	testutil.Equal(t, "an image block is added", read(t, dir, "environments/production/news.yaml"),
		"site: news\nimage:\n  repository: r.example.com/news\n  tag: v1\n")
}

func TestPromoteRefusals(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	before := read(t, dir, "environments/production/shop.yaml")

	// news has no image in its staging file; _shared.yaml's image does not
	// count, and shop must not be written either.
	_, err := run(t, "-f", dir, "promote", "shop", "news", "--to", "production")
	testutil.ErrorIs(t, err, fleet.ErrNoImage)
	testutil.Equal(t, "nothing is written", read(t, dir, "environments/production/shop.yaml"), before)

	// blog's production file cannot be edited in place (a flow mapping
	// without tag): shop, planned first, must not be written either.
	blog := "site: blog\nimage: {repository: r.example.com/blog}\n"
	testutil.WriteFile(t, dir, "environments/production/blog.yaml", blog)
	testutil.WriteFile(t, dir, "environments/staging/blog.yaml",
		"site: blog\nimage:\n  repository: r.example.com/blog\n  tag: v4\n")

	_, err = run(t, "-f", dir, "promote", "shop", "blog", "--to", "production")
	testutil.ErrorIs(t, err, values.ErrUnsupportedLayout)
	testutil.Equal(t, "shop is not written", read(t, dir, "environments/production/shop.yaml"), before)
	testutil.Equal(t, "blog is not written", read(t, dir, "environments/production/blog.yaml"), blog)

	_, err = run(t, "-f", dir, "promote", "shop", "--to", "development")
	testutil.ErrorIs(t, err, fleet.ErrNoPromotionSource)

	_, err = run(t, "-f", dir, "promote", "shop", "--to", "qa")
	testutil.ErrorIs(t, err, fleet.ErrUnknownEnvironment)

	_, err = run(t, "-f", dir, "promote", "--to", "production")
	testutil.ErrorIs(t, err, ops.ErrNoSites)

	_, err = run(t, "-f", dir, "promote", "shop")
	testutil.Error(t, err, "a missing --to")
}
