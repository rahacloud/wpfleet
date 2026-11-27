package deploy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/cmd/deploy"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Test secrets: they must never show up in any output.
const (
	password    = "blogpassword0123456789"
	newPassword = "rotatedpassword0123456"
	salt        = "salt-that-must-stay-hidden"
)

// Command and environment names the tests repeat.
const (
	deployCmd = "deploy"
	diffCmd   = "diff"
	envName   = "staging"
)

// errDeploy is what the fake fails a deploy with.
var errDeploy = errors.New("fake deploy failure")

// fakeReleases renders with the real chart and keeps releases in memory.
type fakeReleases struct {
	helm *helm.Client

	mu    sync.Mutex
	live  map[string]string
	revs  map[string]int
	waits map[string]bool
	fail  map[string]bool
	// descriptions are the release descriptions of the last deploys.
	descriptions map[string]string
}

func newFake(t *testing.T) *fakeReleases {
	t.Helper()

	ch, err := helm.LoadChart("")
	testutil.NoError(t, err)

	return &fakeReleases{
		helm:  helm.New(ch, kube.Target{Kubeconfig: "", Context: "", Namespace: "wp-staging"}),
		mu:    sync.Mutex{},
		live:  map[string]string{},
		revs:  map[string]int{},
		waits: map[string]bool{},
		fail:  map[string]bool{},

		descriptions: map[string]string{},
	}
}

func (f *fakeReleases) Render(ctx context.Context, site, namespace string, files []string) (string, error) {
	out, err := f.helm.Render(ctx, site, namespace, files)
	if err != nil {
		return "", fmt.Errorf("fake render: %w", err)
	}

	return out, nil
}

func (f *fakeReleases) UpgradeInstall(
	ctx context.Context, site, namespace string, files []string, opts helm.InstallOptions,
) (helm.Release, error) {
	f.mu.Lock()
	fail := f.fail[site]
	f.mu.Unlock()

	if fail {
		return helm.Release{}, errDeploy
	}

	manifest, err := f.Render(ctx, site, namespace, files)
	if err != nil {
		return helm.Release{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.live[site] = manifest
	f.revs[site]++
	f.waits[site] = opts.Wait
	f.descriptions[site] = opts.Description

	return helm.Release{
		Name: site, Namespace: namespace, Revision: f.revs[site], Status: "deployed",
		ChartVersion: "0.1.0", Updated: time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), Description: opts.Description,
	}, nil
}

func (f *fakeReleases) LiveManifest(site, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	m, ok := f.live[site]
	if !ok {
		return "", fmt.Errorf("reading release %s: %w", site, helm.ErrReleaseNotFound)
	}

	return m, nil
}

// siteFile is a site file the chart renders.
func siteFile(name, pw string) string {
	return fmt.Sprintf(`site: %s
database:
  password: %s
salts:
  authKey: %s
  secureAuthKey: b
  loggedInKey: c
  nonceKey: d
  authSalt: e
  secureAuthSalt: f
  loggedInSalt: g
  nonceSalt: h
`, name, pw, salt)
}

// fleetDir writes a fleet with blog and shop in staging, and a kubeconfig
// nothing contacts.
func fleetDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range map[string]string{
		"wpfleet.yaml": `environments:
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
		"environments/staging/blog.yaml":    siteFile("blog", password),
		"environments/staging/shop.yaml":    siteFile("shop", password),
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// run runs a root with the global flags, deploy and diff on f, and returns
// the exit code, stdout and stderr.
func run(t *testing.T, f *fakeReleases, args ...string) (int, string, string) {
	t.Helper()

	releases := func(*cmdutil.Env) (deploy.Releases, error) { return f, nil }

	var stdout, stderr bytes.Buffer

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &stdout,
		ErrWriter:      &stderr,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{deploy.CommandWith(releases), deploy.DiffCommandWith(releases)},
	}

	code := cmd.Run(t.Context(), root, append([]string{"wpfleet"}, args...))

	return code, stdout.String(), stderr.String()
}

// noSecrets fails when any output holds a test secret.
func noSecrets(t *testing.T, outputs ...string) {
	t.Helper()

	for _, out := range outputs {
		for _, secret := range []string{password, newPassword, salt} {
			testutil.NotContains(t, "output", out, secret)
		}
	}
}

func TestDeploy(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	dir := fleetDir(t)

	code, out, errOut := run(t, f, "-f", dir, "-e", envName, deployCmd, "--all", "--wait")
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "table", out, "SITE", "REVISION", "blog", "shop", "deployed")
	testutil.Contains(t, "progress", errOut, "deploying blog to staging", "deploying shop to staging")
	testutil.True(t, "waited", f.waits["blog"] && f.waits["shop"])
	testutil.Equal(t, "a fleet outside Git records no commit", f.descriptions["blog"], "wpfleet")
	noSecrets(t, out, errOut)

	code, out, _ = run(t, f, "-f", dir, "-e", envName, "-o", "json", deployCmd, "blog")
	testutil.Equal(t, "exit code", code, 0)

	var results []deploy.Result

	testutil.NoError(t, json.Unmarshal([]byte(out), &results))
	testutil.Equal(t, "one site", len(results), 1)
	testutil.Equal(t, "revision", results[0].Revision, 2)
	testutil.True(t, "no wait", !f.waits["blog"])
}

func TestDeployFailure(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	f.fail["blog"] = true

	code, out, errOut := run(t, f, "-f", fleetDir(t), "-e", envName, deployCmd, "blog", "shop")
	testutil.Equal(t, "exit code", code, 1)
	testutil.Contains(t, "table", out, "failed", "fake deploy failure", "shop")
	testutil.Contains(t, "error", errOut, "wpfleet: sites failed: 1 of 2 (blog)")
	testutil.Equal(t, "shop still deployed", f.revs["shop"], 1)
}

func TestDeployArguments(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	dir := fleetDir(t)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-f", dir, "-e", envName, deployCmd}, ops.ErrNoSites.Error()},
		{[]string{"-f", dir, "-e", envName, deployCmd, "blog", "--all"}, ops.ErrSitesAndAll.Error()},
		{[]string{"-f", dir, "-e", envName, diffCmd, "news"}, "unknown site"},
		{[]string{"-f", dir, deployCmd, "blog"}, cmdutil.ErrNoEnv.Error()},
	} {
		code, _, errOut := run(t, f, tc.args...)
		testutil.Equal(t, strings.Join(tc.args, " ")+" exit code", code, 1)
		testutil.Contains(t, strings.Join(tc.args, " "), errOut, tc.want)
	}

	testutil.Equal(t, "nothing deployed", len(f.live), 0)
}

func TestDiff(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	dir := fleetDir(t)

	// No release yet: everything is new.
	code, out, errOut := run(t, f, "-f", dir, "-e", envName, diffCmd, "blog")
	testutil.Equal(t, "new site exit code", code, 1)
	testutil.Equal(t, "silent", errOut, "")
	testutil.Contains(t, "new site", out, "--- staging/blog (live)", "+++ staging/blog (rendered)", "+kind: Deployment")
	noSecrets(t, out)

	code, _, _ = run(t, f, "-f", dir, "-e", envName, deployCmd, "--all")
	testutil.Equal(t, "deploy exit code", code, 0)

	code, out, errOut = run(t, f, "-f", dir, "-e", envName, diffCmd, "--all")
	testutil.Equal(t, "no difference exit code", code, 0)
	testutil.Equal(t, "no difference", out+errOut, "")

	// Rotating the password changes the Secret, which shows only as tags.
	rotated := siteFile("blog", newPassword) + "php:\n  memoryLimit: 512M\n"
	testutil.WriteFile(t, dir, "environments/staging/blog.yaml", rotated)

	code, out, errOut = run(t, f, "-f", dir, "-e", envName, diffCmd, "--all")
	testutil.Equal(t, "changed exit code", code, 1)
	testutil.Equal(t, "silent", errOut, "")
	testutil.Contains(t, "changed", out, "staging/blog (live)", "WORDPRESS_DB_PASSWORD: (redacted ", "512M")
	testutil.NotContains(t, "shop is unchanged", out, "staging/shop")
	noSecrets(t, out)

	code, out, _ = run(t, f, "-f", dir, "-e", envName, "-o", "json", diffCmd, "--all")
	testutil.Equal(t, "json exit code", code, 1)

	var diffs []deploy.Diff

	testutil.NoError(t, json.Unmarshal([]byte(out), &diffs))
	testutil.Equal(t, "sites", len(diffs), 2)
	testutil.True(t, "blog changed, shop not", diffs[0].Changed && diffs[0].Installed && !diffs[1].Changed)
	noSecrets(t, out)
}

func TestDiffRenderError(t *testing.T) {
	t.Parallel()

	f := newFake(t)
	dir := fleetDir(t)
	testutil.WriteFile(t, dir, "environments/staging/shop.yaml", "site: shop\n")

	code, out, errOut := run(t, f, "-f", dir, "-e", envName, diffCmd, "--all")
	testutil.Equal(t, "exit code", code, 1)
	testutil.Contains(t, "blog still diffed", out, "staging/blog (rendered)")
	testutil.Contains(t, "error", errOut, "staging/shop: ", "wpfleet: sites failed: 1 of 2 (shop)")
}
