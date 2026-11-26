package values_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func environment(name, dir string) fleet.Environment {
	return fleet.Environment{
		Name: name, Namespace: "wp-" + name, Kubeconfig: "", Context: "", ValuesDir: dir, PromoteFrom: "",
	}
}

func TestSites(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteFile(t, dir, "_shared.yaml", "domainSuffix: example.com\n")
	testutil.WriteFile(t, dir, "shop.yaml", "site: shop\n")
	testutil.WriteFile(t, dir, "blog.yaml", "site: blog\n")
	testutil.NoError(t, os.Mkdir(filepath.Join(dir, "old.yaml"), 0o700))

	sites, err := values.Sites(environment("staging", dir))
	testutil.NoError(t, err)
	testutil.Equal(t, "sites", len(sites), 2)
	testutil.Equal(t, "first", sites[0].Name, "blog")
	testutil.Equal(t, "second", sites[1].Name, "shop")

	v, err := values.Load(sites[0].ValuesFiles...)
	testutil.NoError(t, err)

	host, err := fleet.Host(v)
	testutil.NoError(t, err)
	testutil.Equal(t, "host", host, "blog.example.com")

	_, err = values.Sites(environment("gone", filepath.Join(dir, "missing")))
	testutil.ErrorIs(t, err, os.ErrNotExist)

	testutil.WriteFile(t, dir, "Bad.yaml", "site: bad\n")

	_, err = values.Sites(environment("staging", dir))
	testutil.ErrorIs(t, err, fleet.ErrInvalidSiteName)
}
