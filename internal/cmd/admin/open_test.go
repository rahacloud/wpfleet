package admin_test

import (
	"testing"

	"github.com/rahacloud/wpfleet/internal/cmd/admin"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestOpenCommand(t *testing.T) {
	t.Parallel()

	const url = "https://blog.example.com/?wpfleet-login=x"

	testutil.DeepEqual(t, "linux", admin.OpenCommand("linux", url), []string{"xdg-open", url})
	testutil.DeepEqual(t, "freebsd", admin.OpenCommand("freebsd", url), []string{"xdg-open", url})
	testutil.DeepEqual(t, "darwin", admin.OpenCommand("darwin", url), []string{"open", url})
	testutil.DeepEqual(t, "windows", admin.OpenCommand("windows", url),
		[]string{"rundll32", "url.dll,FileProtocolHandler", url})
}
