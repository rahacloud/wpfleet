package sites_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"

	"github.com/rahacloud/wpfleet/internal/cmd"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/cmd/sites"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	staging      = "wp-staging"
	password     = "blogpassword0123456789"
	blog         = "blog"
	envStaging   = "staging"
	groupCmd     = "sites"
	showCmd      = "show"
	defaultImage = "docker.io/library/wordpress:7.0.2-php8.4-apache"
)

// errUnreachable is what an unreachable fake cluster fails with.
var errUnreachable = errors.New("cluster unreachable")

// backupTime is when blog's backup last succeeded.
func backupTime() time.Time {
	return time.Date(2026, 11, 26, 3, 12, 0, 0, time.UTC)
}

// fakeCluster gives a kubetest fake the clientset the sites commands read.
type fakeCluster struct {
	*kubetest.Fake
}

//nolint:ireturn // Cluster requires the clientset interface
func (c fakeCluster) Clientset() kubernetes.Interface { return c.Fake.Clientset }

// failingCluster fails every pod listing.
type failingCluster struct {
	fakeCluster
}

func (failingCluster) SitePods(context.Context, string) ([]corev1.Pod, error) {
	return nil, errUnreachable
}

// fakeReleases answers Status from a map.
type fakeReleases map[string]helm.Release

func (f fakeReleases) Status(site, _ string) (helm.Release, error) {
	r, ok := f[site]
	if !ok {
		return helm.Release{}, fmt.Errorf("reading release %s: %w", site, helm.ErrReleaseNotFound)
	}

	return r, nil
}

// fleetDir writes a fleet with blog and shop in staging and blog in
// production, whose kubeconfig does not exist.
func fleetDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	for name, content := range map[string]string{
		"wpfleet.yaml": `environments:
  - name: staging
    namespace: wp-staging
    kubeconfig: kubeconfig
  - name: production
    namespace: wp-production
    kubeconfig: missing-kubeconfig
`,
		"kubeconfig": `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://k8s.example.com:6443"}}]
users: [{name: u, user: {token: not-a-real-token}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`,
		"environments/staging/_shared.yaml": "domainSuffix: staging.example.com\n" +
			"image:\n  repository: docker.io/library/wordpress\n",
		"environments/staging/blog.yaml": "site: blog\ndatabase:\n  password: " + password + "\n" +
			"image:\n  repository: registry.example.com/sites/blog\n  tag: \"20261126-101500\"\nbackup:\n  enabled: true\n",
		"environments/staging/shop.yaml":      "site: shop\ndomains: [shop.example.com, www.shop.example.com]\n",
		"environments/production/blog.yaml":   "site: blog\ndomains: [blog.example.com]\n",
		"environments/production/notes.txt":   "not a site\n",
		"environments/production/_shared.yml": "ignored: true\n",
	} {
		testutil.WriteFile(t, dir, name, content)
	}

	return dir
}

// stagingObjects are the objects in the staging namespace.
func stagingObjects() []runtime.Object {
	running := kubetest.RunningPod(staging, blog, "blog-7d9f-abcde")
	running.Spec.NodeName = "node-1"

	done := kubetest.RunningPod(staging, blog, "blog-old")
	done.Status.Phase = corev1.PodSucceeded

	backupPod := kubetest.RunningPod(staging, blog, "blog-backup-123")
	backupPod.Labels[kube.LabelComponent] = kube.ComponentBackup

	starting := kubetest.RunningPod(staging, "shop", "shop-5c6d-fghij")
	starting.Status.Conditions = nil

	class := "standard"

	//nolint:exhaustruct_v5 // a test claim needs only what sites show reads
	pvc := &corev1.PersistentVolumeClaim{
		Name:      "blog-uploads",
		Namespace: staging,
		Spec:      corev1.PersistentVolumeClaimSpec{StorageClassName: &class},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    corev1.ClaimBound,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}

	//nolint:exhaustruct_v5 // a test CronJob needs only what sites show reads
	cronJob := &batchv1.CronJob{
		Name:      "blog-backup",
		Namespace: staging,
		Status:    batchv1.CronJobStatus{LastSuccessfulTime: &metav1.Time{Time: backupTime()}},
	}

	return []runtime.Object{running, done, backupPod, starting, pvc, cronJob}
}

// run runs a root with the global flags and the sites group on cluster, and
// returns the exit code, stdout and stderr.
func run(t *testing.T, cluster sites.Cluster, args ...string) (int, string, string) {
	t.Helper()

	releases := fakeReleases{blog: {
		Name: blog, Namespace: staging, Revision: 3, Status: "deployed", ChartVersion: "0.1.0",
		Updated: time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC),
	}}

	var stdout, stderr bytes.Buffer

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Writer:         &stdout,
		ErrWriter:      &stderr,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{sites.CommandWith(
			func(env *cmdutil.Env) (sites.Cluster, error) {
				testutil.Equal(t, "only staging opens", env.Env.Name, envStaging)

				return cluster, nil
			},
			func(*cmdutil.Env) (sites.Releases, error) { return releases, nil },
		)},
	}

	code := cmd.Run(t.Context(), root, append([]string{"wpfleet"}, args...))

	return code, stdout.String(), stderr.String()
}

func TestList(t *testing.T) {
	t.Parallel()

	dir := fleetDir(t)
	cluster := fakeCluster{kubetest.New(staging, stagingObjects()...)}

	// production's kubeconfig is missing: its readiness is unknown, and the
	// rest still lists.
	code, out, _ := run(t, cluster, "-f", dir, "-o", "json", groupCmd, "list")
	testutil.Equal(t, "exit code", code, 0)

	var rows []sites.Row

	testutil.NoError(t, json.Unmarshal([]byte(out), &rows))
	testutil.DeepEqual(t, "rows", rows, []sites.Row{
		{
			Site: blog, Env: envStaging, Host: "blog.staging.example.com",
			Image: "registry.example.com/sites/blog:20261126-101500", Ready: "1/1",
		},
		{Site: "shop", Env: envStaging, Host: "shop.example.com", Image: defaultImage, Ready: "0/1"},
		{Site: blog, Env: "production", Host: "blog.example.com", Image: defaultImage, Ready: "unknown"},
	})
	testutil.NotContains(t, "secrets", out, password)

	code, out, _ = run(t, cluster, "-f", dir, "-e", envStaging, groupCmd, "list")
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "table", out, "SITE", "READY", "blog.staging.example.com", "1/1")
	testutil.NotContains(t, "one environment", out, "production")
}

func TestListUnreachable(t *testing.T) {
	t.Parallel()

	cluster := failingCluster{fakeCluster{kubetest.New(staging)}}

	code, out, _ := run(t, cluster, "-f", fleetDir(t), "-e", envStaging, groupCmd, "list")
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "unknown", out, "blog", "shop", "unknown")

	code, _, errOut := run(t, cluster, "-f", fleetDir(t), "-e", "nowhere", groupCmd, "list")
	testutil.Equal(t, "unknown env exit code", code, 1)
	testutil.Contains(t, "unknown env", errOut, "unknown environment")
}

func TestShow(t *testing.T) {
	t.Parallel()

	dir := fleetDir(t)
	cluster := fakeCluster{kubetest.New(staging, stagingObjects()...)}

	code, out, _ := run(t, cluster, "-f", dir, "-e", envStaging, groupCmd, showCmd, blog)
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "table", out,
		"FIELD", "VALUE",
		"blog.staging.example.com",
		"registry.example.com/sites/blog:20261126-101500",
		"revision 3, deployed, chart 0.1.0, 2026-11-25 09:30:00 UTC",
		"blog-7d9f-abcde (Running, ready); blog-old (Succeeded, ready)",
		"node-1",
		"blog-uploads (Bound, 10Gi, standard)",
		"2026-11-26 03:12:00 UTC",
	)
	testutil.NotContains(t, "backup pod", out, "blog-backup-123")
	testutil.NotContains(t, "secrets", out, password)

	code, out, _ = run(t, cluster, "-f", dir, "-e", envStaging, "-o", "json", groupCmd, showCmd, "shop")
	testutil.Equal(t, "exit code", code, 0)

	var d sites.Detail

	testutil.NoError(t, json.Unmarshal([]byte(out), &d))
	testutil.True(t, "not installed", d.Release == nil)
	testutil.Equal(t, "claim", d.Volume.Claim, "shop-uploads")
	testutil.True(t, "claim missing", !d.Volume.Found)
	testutil.True(t, "backup disabled", !d.Backup.Enabled)
	testutil.Equal(t, "pod not ready", len(d.Pods) == 1 && !d.Pods[0].Ready, true)

	code, out, _ = run(t, cluster, "-f", dir, "-e", envStaging, groupCmd, showCmd, "shop")
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "table", out, "not installed", "shop-uploads (not found)", "disabled", "(Running, not ready)")
}

func TestShowErrors(t *testing.T) {
	t.Parallel()

	dir := fleetDir(t)
	cluster := fakeCluster{kubetest.New(staging)}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-f", dir, "-e", envStaging, groupCmd, showCmd}, sites.ErrOneSite.Error()},
		{[]string{"-f", dir, "-e", envStaging, groupCmd, showCmd, blog, "shop"}, sites.ErrOneSite.Error()},
		{[]string{"-f", dir, groupCmd, showCmd, blog}, cmdutil.ErrNoEnv.Error()},
		{[]string{"-f", dir, "-e", envStaging, groupCmd, showCmd, "news"}, "unknown site"},
	} {
		code, _, errOut := run(t, cluster, tc.args...)
		testutil.Equal(t, "exit code", code, 1)
		testutil.Contains(t, "error", errOut, tc.want)
	}

	code, _, errOut := run(t, failingCluster{cluster}, "-f", dir, "-e", envStaging, groupCmd, showCmd, blog)
	testutil.Equal(t, "unreachable exit code", code, 1)
	testutil.Contains(t, "unreachable", errOut, "cluster unreachable")

	// A backup CronJob that never succeeded, and one that is missing.
	//nolint:exhaustruct_v5 // a CronJob that never ran
	never := &batchv1.CronJob{Name: "blog-backup", Namespace: staging}

	code, out, _ := run(t, fakeCluster{kubetest.New(staging, never)}, "-f", dir, "-e", envStaging, groupCmd, showCmd, blog)
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "never", out, "last backup  never", "pods         none")

	code, out, _ = run(t, cluster, "-f", dir, "-e", envStaging, groupCmd, showCmd, blog)
	testutil.Equal(t, "exit code", code, 0)
	testutil.Contains(t, "missing CronJob", out, "no CronJob blog-backup")
}
