package cmd_test

import (
	"testing"

	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestSitesDeployRegistered(t *testing.T) {
	t.Parallel()

	code, out, _ := run(t, nil, "--help")
	testutil.Equal(t, "--help exit code", code, 0)
	testutil.Contains(t, "commands", out, "sites", "deploy", "diff")

	code, out, _ = run(t, nil, "sites", "--help")
	testutil.Equal(t, "sites --help exit code", code, 0)
	testutil.Contains(t, "sites commands", out, "list", "show")
}
