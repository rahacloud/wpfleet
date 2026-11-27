package helm_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	releasei "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"

	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	site      = "example-site"
	namespace = "wp-staging"
	chartDir  = "../../../charts/wordpress"
)

// testValues is the chart's own lint values.
func testValues() []string {
	return []string{filepath.Join(chartDir, "ci", "test-values.yaml")}
}

func embedded(t *testing.T) *chartv2.Chart {
	t.Helper()

	ch, err := helm.LoadChart("")
	testutil.NoError(t, err)

	return ch
}

func TestLoadChart(t *testing.T) {
	t.Parallel()

	ch := embedded(t)
	testutil.Equal(t, "name", ch.Metadata.Name, "wordpress")

	for _, f := range ch.Raw {
		testutil.True(t, f.Name+" is in .helmignore", !strings.HasPrefix(f.Name, "ci/") && !strings.HasSuffix(f.Name, ".md"))
	}

	fromDir, err := helm.LoadChart(chartDir)
	testutil.NoError(t, err)
	testutil.Equal(t, "version", fromDir.Metadata.Version, ch.Metadata.Version)
	testutil.Equal(t, "templates", len(fromDir.Templates), len(ch.Templates))

	_, err = helm.LoadChart(filepath.Join(t.TempDir(), "missing"))
	testutil.Error(t, err, "missing chart")
}

func TestRender(t *testing.T) {
	t.Parallel()

	client := helm.New(embedded(t), kube.Target{Kubeconfig: "", Context: "", Namespace: namespace})
	ctx := t.Context()

	manifest, err := client.Render(ctx, site, namespace, testValues())
	testutil.NoError(t, err)
	testutil.Contains(t, "manifest", manifest,
		"kind: Deployment", "kind: Service", "kind: Ingress", "kind: PersistentVolumeClaim",
		"example-site.dev.example.invalid")
	testutil.NotContains(t, "manifest", manifest, "helm.sh/hook")
	testutil.True(t, "several documents", len(helm.SplitManifest(manifest)) >= 4)
	testutil.True(t, "chart", client.Chart() != nil)

	bad := testutil.WriteFile(t, t.TempDir(), "bad.yaml", "site: Not_A_Label\n")

	_, err = client.Render(ctx, "x", namespace, append(testValues(), bad))
	testutil.Error(t, err, "the chart's validation")

	_, err = client.Render(ctx, "x", namespace, []string{filepath.Join(t.TempDir(), "missing.yaml")})
	testutil.Error(t, err, "missing values file")
}

// The time zones are values with neutral defaults, and the backup CronJob
// runs beside the site's pod, which holds the uploads volume.
func TestRenderTimeZonesAndBackup(t *testing.T) {
	t.Parallel()

	client := helm.New(embedded(t), kube.Target{Kubeconfig: "", Context: "", Namespace: namespace})
	backup := testutil.WriteFile(t, t.TempDir(), "backup.yaml",
		"backup:\n  enabled: true\n  s3:\n    endpoint: https://s3.example.com\n    bucket: backups\n")

	manifest, err := client.Render(t.Context(), site, namespace, append(testValues(), backup))
	testutil.NoError(t, err)
	testutil.Contains(t, "manifest", manifest,
		"date.timezone = UTC\n", `timeZone: "Etc/UTC"`,
		"podAffinity:", "topologyKey: kubernetes.io/hostname", "app.kubernetes.io/instance: "+site)

	zones := testutil.WriteFile(t, t.TempDir(), "zones.yaml",
		"php:\n  timezone: Europe/Berlin\nbackup:\n  timeZone: \"\"\n")

	manifest, err = client.Render(t.Context(), site, namespace, append(testValues(), backup, zones))
	testutil.NoError(t, err)
	testutil.Contains(t, "manifest", manifest, "date.timezone = Europe/Berlin\n")
	testutil.NotContains(t, "manifest", manifest, "timeZone:")
}

func TestUpgradeInstall(t *testing.T) {
	t.Parallel()

	ch := embedded(t)
	client := helm.NewInMemory(ch)
	ctx := t.Context()

	_, err := client.Status(site, namespace)
	testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)

	_, err = client.LiveManifest(site, namespace)
	testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)

	rel, err := client.UpgradeInstall(ctx, site, namespace, testValues(),
		helm.InstallOptions{Wait: false, Description: ""})
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "install", []string{rel.Status, rel.ChartVersion, rel.Namespace},
		[]string{"deployed", ch.Metadata.Version, namespace})
	testutil.Equal(t, "install revision", rel.Revision, 1)

	rel, err = client.UpgradeInstall(ctx, site, namespace, testValues(),
		helm.InstallOptions{Wait: true, Description: "wpfleet 0123abc"})
	testutil.NoError(t, err)
	testutil.Equal(t, "upgrade revision", rel.Revision, 2)
	testutil.True(t, "updated", !rel.Updated.IsZero())

	status, err := client.Status(site, namespace)
	testutil.NoError(t, err)
	testutil.Equal(t, "status revision", status.Revision, 2)
	testutil.Equal(t, "description", status.Description, "wpfleet 0123abc")

	live, err := client.LiveManifest(site, namespace)
	testutil.NoError(t, err)

	rendered, err := client.Render(ctx, site, namespace, testValues())
	testutil.NoError(t, err)
	testutil.Equal(t, "live manifest of an unchanged release", live, rendered)

	_, err = client.UpgradeInstall(ctx, site, namespace, []string{filepath.Join(t.TempDir(), "missing.yaml")},
		helm.InstallOptions{Wait: false, Description: ""})
	testutil.Error(t, err, "missing values file")
}

// diff compares with what the cluster runs: the last deployed revision, not
// a newer one that failed.
func TestLiveManifestIsTheDeployedRevision(t *testing.T) {
	t.Parallel()

	client := helm.NewInMemory(embedded(t))
	ctx := t.Context()

	_, err := client.UpgradeInstall(ctx, site, namespace, testValues(), helm.InstallOptions{Wait: false, Description: ""})
	testutil.NoError(t, err)

	deployed, err := client.LiveManifest(site, namespace)
	testutil.NoError(t, err)

	testutil.NoError(t, client.Store(namespace, record(2, common.StatusFailed, "kind: NeverApplied")))

	live, err := client.LiveManifest(site, namespace)
	testutil.NoError(t, err)
	testutil.Equal(t, "the deployed revision's manifest", live, deployed)

	// A release whose only revision failed, or was uninstalled, runs nothing.
	for _, status := range []common.Status{common.StatusFailed, common.StatusUninstalled} {
		other := helm.NewInMemory(embedded(t))
		testutil.NoError(t, other.Store(namespace, record(1, status, "kind: Gone")))

		_, err = other.LiveManifest(site, namespace)
		testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)
	}
}

// The storage lists revisions in its own order; the newest is the one with
// the highest number, so v12 wins over v9 and v10.
func TestNewest(t *testing.T) {
	t.Parallel()

	versions := []releasei.Releaser{
		record(10, common.StatusSuperseded, ""),
		record(12, common.StatusUninstalled, ""),
		record(9, common.StatusSuperseded, ""),
	}
	testutil.Equal(t, "newest", helm.Newest(versions).Version, 12)
	testutil.True(t, "none", helm.Newest(nil) == nil)
}

// record is a stored revision of the test site.
func record(version int, status common.Status, manifest string) *releasev1.Release {
	//nolint:exhaustruct_v5 // a stored record needs only these
	return &releasev1.Release{
		Name:      site,
		Namespace: namespace,
		Version:   version,
		Manifest:  manifest,
		Info:      &releasev1.Info{Status: status},
	}
}

func TestSplitManifest(t *testing.T) {
	t.Parallel()

	docs := helm.SplitManifest("---\n# Source: a\nkind: A\n---\n\n---\nkind: B\n")
	testutil.DeepEqual(t, "documents", docs, []string{"# Source: a\nkind: A", "kind: B"})
}

// History is newest first by number, whatever order the storage keeps, and
// carries each revision's description.
func TestHistory(t *testing.T) {
	t.Parallel()

	client := helm.NewInMemory(embedded(t))

	_, err := client.History(site, namespace, 10)
	testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)

	for _, v := range []int{9, 11, 10} {
		r := record(v, common.StatusSuperseded, "")
		r.Info.Description = "wpfleet rev" + strconv.Itoa(v)
		testutil.NoError(t, client.Store(namespace, r))
	}

	all, err := client.History(site, namespace, 0)
	testutil.NoError(t, err)
	testutil.Equal(t, "revisions", len(all), 3)
	testutil.DeepEqual(t, "order", []int{all[0].Revision, all[1].Revision, all[2].Revision}, []int{11, 10, 9})
	testutil.Equal(t, "description", all[0].Description, "wpfleet rev11")
	testutil.Equal(t, "status", all[0].Status, "superseded")

	two, err := client.History(site, namespace, 2)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "limited", []int{two[0].Revision, two[1].Revision}, []int{11, 10})
}
