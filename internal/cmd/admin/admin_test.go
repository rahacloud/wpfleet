package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/admin"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const namespace = "wp-staging"

// fleetDir writes a fleet with blog and shop in staging, and a kubeconfig
// that nothing contacts.
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
		"environments/staging/blog.yaml":    "site: blog\ndatabase:\n  password: secretpassword1\n",
		"environments/staging/shop.yaml": "site: shop\ndomains: [shop.example.com]\n" +
			"ingress:\n  tls:\n    enabled: false\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// result is what one run of the command did.
type result struct {
	stdout string
	opened []string
	wp     *fakeWP
}

// runAdmin runs wpfleet with the global flags and admin with args, against a
// fake cluster where blog and shop each have a running pod.
func runAdmin(t *testing.T, globals []string, args ...string) (result, error) {
	t.Helper()

	return runAdminIn(t, fleetDir(t), globals, args...)
}

// runAdminIn is runAdmin on the fleet in dir.
func runAdminIn(t *testing.T, dir string, globals []string, args ...string) (result, error) {
	t.Helper()

	pods := kubetest.New(namespace,
		kubetest.RunningPod(namespace, "blog", "blog-0"), kubetest.RunningPod(namespace, "shop", "shop-0"))
	res := result{stdout: "", opened: nil, wp: newFakeWP(singleSite())}
	open := func(_ context.Context, url string) error {
		res.opened = append(res.opened, url)

		return nil
	}

	var stdout bytes.Buffer

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &stdout,
		ErrWriter:      &bytes.Buffer{},
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			admin.NewTestCommand(pods, res.wp, open, seed()),
		},
	}

	argv := append([]string{"wpfleet", "--fleet", dir, "--env", "staging"}, globals...)

	err := root.Run(t.Context(), append(append(argv, "admin"), args...))
	res.stdout = stdout.String()

	if err != nil {
		return res, fmt.Errorf("running: %w", err)
	}

	return res, nil
}

func TestCommand(t *testing.T) {
	t.Parallel()

	token, err := admin.NewToken(seed())
	testutil.NoError(t, err)

	res, err := runAdmin(t, nil, "blog")
	testutil.NoError(t, err)
	testutil.Equal(t, "stdout", res.stdout, "https://blog.staging.example.com/?wpfleet-login="+token+"\n")
	testutil.Equal(t, "opened", len(res.opened), 0)
	testutil.NotContains(t, "stdout", res.stdout, "secretpassword1")
	testutil.Equal(t, "pod", res.wp.call(t, "option", "add")[0], "blog-0")

	_, stored := res.wp.stored(t)
	testutil.Equal(t, "expiry", stored.Expires, int64(1_800_000_060))

	res, err = runAdmin(t, nil, "--open", "--user", "editor", "shop")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "opened", res.opened, []string{"http://shop.example.com/?wpfleet-login=" + token})
	testutil.Equal(t, "user", res.wp.call(t, "user", "get")[3], "editor")
	testutil.Equal(t, "pod", res.wp.call(t, "user", "get")[0], "shop-0")
}

// The host and the scheme come from the values over the chart's defaults, so
// a fleet's own chart that sets domainSuffix and turns TLS off is followed.
func TestCommandChartDefaults(t *testing.T) {
	t.Parallel()

	dir := fleetDir(t)
	testutil.CustomChart(t, dir, "charts/custom",
		`domainSuffix: ""`, `domainSuffix: example.org`,
		"tls:\n    enabled: true", "tls:\n    enabled: false")
	testutil.WriteFile(t, dir, "wpfleet.yaml",
		"chart: charts/custom\nenvironments:\n  - name: staging\n    namespace: wp-staging\n    kubeconfig: kubeconfig\n")
	testutil.WriteFile(t, dir, "environments/staging/_shared.yaml", "{}\n")

	res, err := runAdminIn(t, dir, nil, "blog")
	testutil.NoError(t, err)
	testutil.Contains(t, "stdout", res.stdout, "http://blog.example.org/?wpfleet-login=")
}

func TestCommandJSON(t *testing.T) {
	t.Parallel()

	res, err := runAdmin(t, []string{"--output", "json"}, "blog")
	testutil.NoError(t, err)

	var logins []admin.Login
	testutil.NoError(t, json.Unmarshal([]byte(res.stdout), &logins))
	testutil.Equal(t, "logins", len(logins), 1)
	testutil.Equal(t, "user", logins[0].UserID, 7)
	testutil.Equal(t, "expires", logins[0].Expires.Unix(), int64(1_800_000_060))
	testutil.Contains(t, "url", logins[0].URL, "https://blog.staging.example.com/?wpfleet-login=")
}

func TestCommandErrors(t *testing.T) {
	t.Parallel()

	_, err := runAdmin(t, nil)
	testutil.ErrorIs(t, err, admin.ErrNoSite)

	_, err = runAdmin(t, nil, "missing")
	testutil.Error(t, err, "unknown site")

	_, err = runAdmin(t, nil, "--user=-x", "blog")
	testutil.ErrorIs(t, err, admin.ErrBadUser)
}
