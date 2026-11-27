package cmdutil_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

type row struct {
	Site string `json:"site"`
	Host string `json:"host"`
}

func columns() []cmdutil.Column[row] {
	return []cmdutil.Column[row]{
		{Header: "site", Value: func(r row) string { return r.Site }},
		{Header: "host", Value: func(r row) string { return r.Host }},
	}
}

func TestWrite(t *testing.T) {
	t.Parallel()

	rows := []row{{Site: "blog", Host: "blog.example.com"}, {Site: "shop-longer", Host: ""}}

	var buf bytes.Buffer

	testutil.NoError(t, cmdutil.Write(&buf, cmdutil.FormatTable, rows, columns()...))
	testutil.Equal(t, "table", buf.String(), "SITE         HOST\nblog         blog.example.com\nshop-longer  -\n")

	buf.Reset()

	testutil.NoError(t, cmdutil.Write(&buf, cmdutil.FormatJSON, rows, columns()...))
	testutil.Contains(t, "json", buf.String(), `"site": "shop-longer"`, "[")

	buf.Reset()

	testutil.NoError(t, cmdutil.Write[row](&buf, cmdutil.FormatJSON, nil, columns()...))
	testutil.Equal(t, "empty json", buf.String(), "[]\n")
}

func TestParseFormat(t *testing.T) {
	t.Parallel()

	f, err := cmdutil.ParseFormat("JSON")
	testutil.NoError(t, err)
	testutil.Equal(t, "format", f, cmdutil.FormatJSON)

	_, err = cmdutil.ParseFormat("yaml")
	testutil.ErrorIs(t, err, cmdutil.ErrUnknownFormat)
}

// fleetDir writes a fleet with two sites in staging and a kubeconfig that
// points nowhere real; nothing here contacts it.
func fleetDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range map[string]string{
		"wpfleet.yaml": `registry: registry.example.com/sites
environments:
  - name: staging
    namespace: wp-staging
    kubeconfig: kubeconfig
`,
		"kubeconfig": `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://k8s.example.com:6443"}}]
users: [{name: u, user: {token: not-a-real-token}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`,
		"environments/staging/_shared.yaml": "domainSuffix: staging.example.com\n",
		"environments/staging/blog.yaml":    "site: blog\n",
		"environments/staging/shop.yaml":    "site: shop\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// openEnv runs cmdutil.OpenEnv under a root command with the global flags.
func openEnv(t *testing.T, args ...string) (*cmdutil.Env, error) {
	t.Helper()

	var env *cmdutil.Env

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &bytes.Buffer{},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Action: func(_ context.Context, cmd *cli.Command) error {
			var err error

			env, err = cmdutil.OpenEnv(cmd)

			return err
		},
	}

	err := root.Run(t.Context(), append([]string{"wpfleet"}, args...))
	if err != nil {
		return nil, fmt.Errorf("running: %w", err)
	}

	return env, nil
}

func TestOpenEnv(t *testing.T) {
	t.Parallel()

	env, err := openEnv(t, "-f", fleetDir(t), "-e", "staging")
	testutil.NoError(t, err)

	testutil.Equal(t, "namespace", env.Env.Namespace, "wp-staging")
	testutil.Equal(t, "sites", len(env.Sites), 2)
	testutil.Equal(t, "kube namespace", env.Kube.Namespace(), "wp-staging")

	site, err := env.Site("blog")
	testutil.NoError(t, err)

	v, err := env.Values(site)
	testutil.NoError(t, err)

	host, err := fleet.Host(v)
	testutil.NoError(t, err)
	testutil.Equal(t, "host", host, "blog.staging.example.com")

	_, err = env.Site("news")
	testutil.ErrorIs(t, err, fleet.ErrUnknownSite)
}

func TestSelectSites(t *testing.T) {
	t.Parallel()

	env, err := openEnv(t, "-f", fleetDir(t), "-e", "staging")
	testutil.NoError(t, err)

	all, err := env.SelectSites(nil, true)
	testutil.NoError(t, err)
	testutil.Equal(t, "all", len(all), 2)

	one, err := env.SelectSites([]string{"shop"}, false)
	testutil.NoError(t, err)
	testutil.Equal(t, "named", one[0].Name, "shop")

	_, err = env.SelectSites([]string{"blog"}, true)
	testutil.ErrorIs(t, err, ops.ErrSitesAndAll)

	_, err = env.SelectSites(nil, false)
	testutil.ErrorIs(t, err, ops.ErrNoSites)

	_, err = env.SelectSites([]string{"news"}, false)
	testutil.ErrorIs(t, err, fleet.ErrUnknownSite)
}

func TestHelmThroughEnv(t *testing.T) {
	t.Parallel()

	env, err := openEnv(t, "-f", fleetDir(t), "-e", "staging")
	testutil.NoError(t, err)

	site, err := env.Site("blog")
	testutil.NoError(t, err)

	h, err := env.Helm()
	testutil.NoError(t, err)

	lint := filepath.Join("..", "..", "..", "charts", "wordpress", "ci", "test-values.yaml")

	manifest, err := h.Render(t.Context(), "blog", env.Env.Namespace, append(site.ValuesFiles, lint))
	testutil.NoError(t, err)
	testutil.Contains(t, "manifest", manifest, "kind: Deployment")

	again, err := env.Helm()
	testutil.NoError(t, err)
	testutil.True(t, "the chart loads once", again == h)
}

func TestOpenEnvErrors(t *testing.T) {
	t.Parallel()

	dir := fleetDir(t)

	_, err := openEnv(t, "-f", dir)
	testutil.ErrorIs(t, err, cmdutil.ErrNoEnv)

	_, err = openEnv(t, "-f", dir, "-e", "production")
	testutil.ErrorIs(t, err, fleet.ErrUnknownEnvironment)

	_, err = openEnv(t, "-f", t.TempDir(), "-e", "staging")
	testutil.ErrorIs(t, err, os.ErrNotExist)
}
