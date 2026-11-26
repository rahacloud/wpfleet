package helm_test

import (
	"path/filepath"
	"strings"
	"testing"

	chartv2 "helm.sh/helm/v4/pkg/chart/v2"

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

func TestUpgradeInstall(t *testing.T) {
	t.Parallel()

	ch := embedded(t)
	client := helm.NewInMemory(ch)
	ctx := t.Context()

	_, err := client.Status(site, namespace)
	testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)

	_, err = client.LiveManifest(site, namespace)
	testutil.ErrorIs(t, err, helm.ErrReleaseNotFound)

	rel, err := client.UpgradeInstall(ctx, site, namespace, testValues(), false)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "install", []string{rel.Status, rel.ChartVersion, rel.Namespace},
		[]string{"deployed", ch.Metadata.Version, namespace})
	testutil.Equal(t, "install revision", rel.Revision, 1)

	rel, err = client.UpgradeInstall(ctx, site, namespace, testValues(), true)
	testutil.NoError(t, err)
	testutil.Equal(t, "upgrade revision", rel.Revision, 2)
	testutil.True(t, "updated", !rel.Updated.IsZero())

	status, err := client.Status(site, namespace)
	testutil.NoError(t, err)
	testutil.Equal(t, "status revision", status.Revision, 2)

	live, err := client.LiveManifest(site, namespace)
	testutil.NoError(t, err)

	rendered, err := client.Render(ctx, site, namespace, testValues())
	testutil.NoError(t, err)
	testutil.Equal(t, "live manifest of an unchanged release", live, rendered)

	_, err = client.UpgradeInstall(ctx, site, namespace, []string{filepath.Join(t.TempDir(), "missing.yaml")}, false)
	testutil.Error(t, err, "missing values file")
}

func TestSplitManifest(t *testing.T) {
	t.Parallel()

	docs := helm.SplitManifest("---\n# Source: a\nkind: A\n---\n\n---\nkind: B\n")
	testutil.DeepEqual(t, "documents", docs, []string{"# Source: a\nkind: A", "kind: B"})
}
