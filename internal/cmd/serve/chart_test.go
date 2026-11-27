package serve_test

import (
	"path/filepath"
	"slices"
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"

	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// serverChart is the chart that runs wpfleet serve.
const serverChart = "../../../deployments/helm/wpfleet"

// siteRoles renders the server chart with its lint values and returns the
// Roles it makes in the site namespaces.
func siteRoles(t *testing.T) []rbacv1.Role {
	t.Helper()

	ch, err := helm.LoadChart(serverChart)
	testutil.NoError(t, err)

	client := helm.New(ch, kube.Target{Kubeconfig: "", Context: "", Namespace: "wpfleet"})
	manifest, err := client.Render(t.Context(), "wpfleet", "wpfleet",
		[]string{filepath.Join(serverChart, "ci", "test-values.yaml")})
	testutil.NoError(t, err)

	var roles []rbacv1.Role

	for _, doc := range helm.SplitManifest(manifest) {
		var role rbacv1.Role

		testutil.NoError(t, yaml.Unmarshal([]byte(doc), &role))

		if role.Kind == "Role" {
			roles = append(roles, role)
		}
	}

	return roles
}

// The server only reads pods: the chart makes them through Deployments and
// Jobs, and creating one would let a stolen token run as any ServiceAccount
// of the namespace. exec and logs stay on their subresources.
func TestChartRoleOnlyReadsPods(t *testing.T) {
	t.Parallel()

	roles := siteRoles(t)
	testutil.Equal(t, "a Role per site namespace", len(roles), 2)

	readOnly := []string{"get", "list", "watch"}

	for _, role := range roles {
		var exec, logs bool

		for _, rule := range role.Rules {
			for _, resource := range rule.Resources {
				switch resource {
				case "pods":
					for _, verb := range rule.Verbs {
						testutil.True(t, role.Namespace+": pods "+verb+" is read-only", slices.Contains(readOnly, verb))
					}
				case "pods/exec":
					exec = slices.Contains(rule.Verbs, "create")
				case "pods/log":
					logs = slices.Contains(rule.Verbs, "get")
				}
			}
		}

		testutil.True(t, role.Namespace+": exec", exec)
		testutil.True(t, role.Namespace+": logs", logs)
	}
}

// allows reports whether role grants verb on resource in group.
func allows(role rbacv1.Role, group, resource, verb string) bool {
	return slices.ContainsFunc(role.Rules, func(rule rbacv1.PolicyRule) bool {
		return slices.Contains(rule.APIGroups, group) && slices.Contains(rule.Resources, resource) &&
			slices.Contains(rule.Verbs, verb)
	})
}

// A deploy from the UI waits for readiness, and Helm's status watcher
// computes a Deployment's status from its ReplicaSets and their pods; without
// reading ReplicaSets the status stays Unknown and every deploy times out.
// The server never writes a ReplicaSet, so it only reads them.
func TestChartRoleLetsHelmWait(t *testing.T) {
	t.Parallel()

	for _, role := range siteRoles(t) {
		for _, gr := range [][2]string{
			{"apps", "deployments"}, {"apps", "replicasets"}, {"", "pods"}, {"batch", "jobs"},
			{"", "persistentvolumeclaims"}, {"", "services"}, {"networking.k8s.io", "ingresses"},
		} {
			for _, verb := range []string{"get", "list", "watch"} {
				testutil.True(t, role.Namespace+": "+verb+" "+gr[1], allows(role, gr[0], gr[1], verb))
			}
		}

		for _, verb := range []string{"create", "update", "patch", "delete"} {
			testutil.True(t, role.Namespace+": replicasets are read-only, not "+verb,
				!allows(role, "apps", "replicasets", verb))
		}
	}
}
