package helm_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// TestOneTimeLogin checks that wordpress.oneTimeLogin puts the must-use
// plugin in the ConfigMap and mounts it read-only, and that it is on by
// default.
func TestOneTimeLogin(t *testing.T) {
	t.Parallel()

	client := helm.New(embedded(t), kube.Target{Kubeconfig: "", Context: "", Namespace: namespace})
	plugin := testutil.ReadFile(t, filepath.Join(chartDir, "files", "wpfleet-login.php"))

	manifest, err := client.Render(t.Context(), site, namespace, testValues())
	testutil.NoError(t, err)

	// The ConfigMap holds the file under its key, every line indented the
	// way nindent does it, blank ones included.
	var indented strings.Builder
	for line := range strings.Lines(strings.TrimRight(plugin, "\n")) {
		indented.WriteString("    " + line)
	}

	testutil.Contains(t, "manifest", manifest,
		"  wpfleet-login.php: |\n"+indented.String(),
		"mountPath: /var/www/html/wp-content/mu-plugins/wpfleet-login.php\n"+
			"              subPath: wpfleet-login.php\n"+
			"              readOnly: true\n")

	off := testutil.WriteFile(t, t.TempDir(), "off.yaml", "wordpress:\n  oneTimeLogin: false\n")

	manifest, err = client.Render(t.Context(), site, namespace, append(testValues(), off))
	testutil.NoError(t, err)
	testutil.NotContains(t, "manifest without the login", manifest, "wpfleet-login")
}
