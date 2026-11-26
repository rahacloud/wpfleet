package copy_test

import (
	"strings"
	"testing"

	bt "github.com/rahacloud/wpfleet/internal/cmd/backup/backuptest"
	copycmd "github.com/rahacloud/wpfleet/internal/cmd/copy"
	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/assets"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func objects() map[string]int64 {
	return map[string]int64{
		"wp-staging/blog/database/20261125T031500Z.sql.gz": 100,
		"wp-staging/blog/database/20261126T031500Z.sql.gz": 200,
		"wp-staging/blog/uploads/current/2026/11/a.jpg":    10,
	}
}

// harness has blog running in development.
func harness(t *testing.T) *bt.Harness {
	t.Helper()

	h := bt.New(t, objects())
	h.Seed(t, bt.Development, kubetest.RunningPod(bt.Namespace(bt.Development), "blog", "blog-1"))

	return h
}

// cacheFlush is the last wp-cli command of a copy.
const cacheFlush = "cache flush"

// copyArgs are the arguments of wpfleet copy blog.
func copyArgs(from, to string, extra ...string) []string {
	return append([]string{"copy", "blog", "--from", from, "--to", to}, extra...)
}

// wpCalls are the wp-cli commands run in a cluster, without the phar part.
func wpCalls(f *kubetest.Fake) []string {
	prefix := len(wpcli.Command(""))

	var out []string

	for _, c := range f.Calls() {
		if len(c.Command) > prefix && c.Command[0] == "php" {
			out = append(out, strings.Join(c.Command[prefix:], " "))
		}
	}

	return out
}

func TestCopy(t *testing.T) {
	t.Parallel()

	h := harness(t)

	args := copyArgs(bt.Staging, bt.Development)
	testutil.NoError(t, h.Run(t, copycmd.NewCommand(h.Deps), "y\n", args...))

	jobs := h.Jobs(t, bt.Development)
	testutil.Equal(t, "restore job in the target", len(jobs), 1)
	testutil.Equal(t, "nothing in the source", len(h.Jobs(t, bt.Staging)), 0)

	fetch := jobs[0].Spec.Template.Spec.InitContainers[0]
	dump, _ := bt.Env(fetch, "DUMP")
	testutil.Equal(t, "latest dump of the source", dump, "wp-staging/blog/database/20261126T031500Z.sql.gz")

	uploads, _ := bt.Env(fetch, "UPLOADS")
	testutil.Equal(t, "uploads by default", uploads, "true")

	dev := h.Kube[bt.Development]
	testutil.DeepEqual(t, "wp-cli", wpCalls(dev), []string{
		"search-replace blog-staging.example.com blog-dev.example.com --all-tables-with-prefix --skip-columns=guid --precise",
		"eval-file /tmp/wpfleet-rehost-extra.php blog-staging.example.com blog-dev.example.com",
		cacheFlush,
	})

	php, ok := dev.File("blog-1", "/tmp/wpfleet-"+assets.RehostExtraName)
	testutil.True(t, "fix-up copied in", ok)
	testutil.Equal(t, "fix-up", string(php), string(assets.RehostExtra))

	testutil.Contains(t, "stderr", h.Err.String(),
		"Then moving blog-staging.example.com to blog-dev.example.com.",
		"Overwrite blog in development with staging's? [y/N]",
		"Copied blog into development as blog-dev.example.com.")
}

func TestCopyMultisiteDatabaseOnly(t *testing.T) {
	t.Parallel()

	h := harness(t)
	h.WriteSite(t, bt.Development, "blog", "wordpress:\n  configExtra: |\n    define('MULTISITE', true);\n")
	h.WriteSite(t, bt.Staging, "blog", "ingress:\n  tls:\n    enabled: false\n")

	args := copyArgs(bt.Staging, bt.Development, "--skip-uploads", "--yes")
	testutil.NoError(t, h.Run(t, copycmd.NewCommand(h.Deps), "", args...))

	// The imported database still holds the source's domain, so wp-cli loads
	// the network by the source's URL for search-replace, and by the target's
	// own configuration once the domain is replaced.
	testutil.DeepEqual(t, "wp-cli", wpCalls(h.Kube[bt.Development]), []string{
		"search-replace blog-staging.example.com blog-dev.example.com --all-tables-with-prefix --skip-columns=guid " +
			"--precise --network --url=http://blog-staging.example.com/",
		"eval-file /tmp/wpfleet-rehost-extra.php blog-staging.example.com blog-dev.example.com",
		cacheFlush,
	})

	pod := h.Jobs(t, bt.Development)[0].Spec.Template.Spec
	testutil.Equal(t, "no uploads volume", len(pod.Volumes), 1)
}

// The hosts come from the values over the chart's defaults, so a fleet's own
// chart that sets domainSuffix gives the host the chart renders.
func TestCopyChartDefaults(t *testing.T) {
	t.Parallel()

	h := harness(t)
	testutil.CustomChart(t, h.Dir, "charts/custom", `domainSuffix: ""`, `domainSuffix: example.org`)
	testutil.WriteFile(t, h.Dir, "wpfleet.yaml", "chart: charts/custom\n"+testutil.ReadFile(t, h.Dir+"/wpfleet.yaml"))
	testutil.WriteFile(t, h.Dir, "environments/development/_shared.yaml", "backup:\n  enabled: true\n")

	testutil.NoError(t, h.Run(t, copycmd.NewCommand(h.Deps), "", copyArgs(bt.Staging, bt.Development, "-y")...))
	testutil.Equal(t, "search-replace", wpCalls(h.Kube[bt.Development])[0],
		"search-replace blog-staging.example.com blog.example.org --all-tables-with-prefix --skip-columns=guid --precise")
}

func TestCopySameHost(t *testing.T) {
	t.Parallel()

	h := harness(t)
	h.WriteSite(t, bt.Development, "blog", "domains: [blog-staging.example.com]\n")

	args := copyArgs(bt.Staging, bt.Development, "-y")
	testutil.NoError(t, h.Run(t, copycmd.NewCommand(h.Deps), "", args...))
	testutil.DeepEqual(t, "only the flush", wpCalls(h.Kube[bt.Development]), []string{cacheFlush})
	testutil.Contains(t, "stderr", h.Err.String(), "no host to move")
}

func TestCopyRefuses(t *testing.T) {
	t.Parallel()

	h := harness(t)

	err := h.Run(t, copycmd.NewCommand(h.Deps), "", copyArgs(bt.Staging, bt.Production, "-y")...)
	testutil.ErrorIs(t, err, copycmd.ErrProduction)

	err = h.Run(t, copycmd.NewCommand(h.Deps), "", copyArgs(bt.Staging, bt.Staging, "-y")...)
	testutil.ErrorIs(t, err, copycmd.ErrSameEnvironment)

	err = h.Run(t, copycmd.NewCommand(h.Deps), "", copyArgs(bt.Development, bt.Staging, "-y")...)
	testutil.ErrorIs(t, err, domain.ErrNoBackups)

	err = h.Run(t, copycmd.NewCommand(h.Deps), "no\n", copyArgs(bt.Staging, bt.Development)...)
	testutil.Error(t, err, "not confirmed")

	for env, k := range h.Kube {
		testutil.Equal(t, "no job in "+env, len(h.Jobs(t, env)), 0)
		testutil.True(t, "no exec in "+env, len(k.Calls()) == 0)
	}

	// --yes-production gets past the refusal, but production has no running
	// pod, which is found before anything is overwritten.
	err = h.Run(t, copycmd.NewCommand(h.Deps), "", copyArgs(bt.Staging, bt.Production, "-y", "--yes-production")...)
	testutil.ErrorIs(t, err, kube.ErrNoRunningPod)
	testutil.Equal(t, "nothing restored", len(h.Jobs(t, bt.Production)), 0)
}
