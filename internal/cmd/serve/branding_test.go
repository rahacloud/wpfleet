package serve_test

import (
	"os"
	"testing"

	"github.com/rahacloud/wpfleet/internal/cmd/serve"
	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestCheckBranding(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.WriteFile(t, dir, "brand/logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	testutil.WriteFile(t, dir, "brand/favicon.png", "not a png")

	b := config.DefaultServer().Branding
	testutil.NoError(t, serve.CheckBranding(dir, b))

	b.Logo = "brand/logo.svg"
	testutil.NoError(t, serve.CheckBranding(dir, b))

	b.Favicon = "brand/favicon.png"
	err := serve.CheckBranding(dir, b)
	testutil.ErrorIs(t, err, web.ErrBrandImage)
	testutil.Contains(t, "names the key", err.Error(), "server.branding.favicon")

	b.Favicon = ""
	b.Logo = "brand/missing.webp"
	err = serve.CheckBranding(dir, b)
	testutil.ErrorIs(t, err, os.ErrNotExist)
	testutil.Contains(t, "names the key", err.Error(), "server.branding.logo")
}
