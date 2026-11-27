package ops_test

import (
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// password is a test secret that no normalised manifest may hold.
const password = "blogpassword0123456789"

func TestNormalize(t *testing.T) {
	t.Parallel()

	live := `---
# Source: wordpress/templates/service.yaml
apiVersion: v1
kind: Service

metadata:
  name: blog
---
# Source: wordpress/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: blog
stringData:
  WORDPRESS_DB_PASSWORD: "` + password + `"
data:
  other: c2VjcmV0
`
	rendered := `# Source: wordpress/templates/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: blog
stringData:
  WORDPRESS_DB_PASSWORD: ` + password + `
data:
  other: c2VjcmV0
---

# Source: wordpress/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: blog
`

	got := ops.Normalize(live, rendered, "kind: Secret\nstringData: [")
	for _, g := range got {
		testutil.NotContains(t, "normalised", g, password)
	}
	testutil.NotContains(t, "data", got[0], "c2VjcmV0")
	testutil.Equal(t, "order, quoting and blank lines do not count", got[0], got[1])
	testutil.Contains(t, "redacted", got[0], "WORDPRESS_DB_PASSWORD: (redacted ", "kind: Service")
	testutil.True(t, "Secret sorts before Service", strings.Index(got[0], "Secret") < strings.Index(got[0], "Service"))
	testutil.NotContains(t, "blank lines", got[0], "\n\n")
	testutil.Equal(t, "unparseable Secret", strings.HasPrefix(got[2], "---\n(redacted "), true)
}
