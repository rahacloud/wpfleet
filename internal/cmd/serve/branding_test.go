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

// Every key of the server section's branding reaches the web server, the
// wordmark's flag among them.
func TestWebBranding(t *testing.T) {
	t.Parallel()

	b := config.DefaultServer().Branding
	b.Name = "Northwind Media"
	b.Logo = "brand/logo.svg"
	b.LogoIncludesName = true
	b.Favicon = "brand/favicon.png"
	b.SupportURL = "https://help.example.com/"
	b.SupportEmail = "web-help@example.com"
	b.Welcome = config.Welcome{EN: "Hello.", FA: "سلام."}

	w := serve.WebBranding(b, func() string { return "/fleet" })
	testutil.Equal(t, "name", w.Name, b.Name)
	testutil.Equal(t, "logo", w.Logo, b.Logo)
	testutil.Equal(t, "wordmark", w.LogoIncludesName, true)
	testutil.Equal(t, "favicon", w.Favicon, b.Favicon)
	testutil.Equal(t, "color", w.PrimaryColor, b.PrimaryColor)
	testutil.Equal(t, "support URL", w.SupportURL, b.SupportURL)
	testutil.Equal(t, "support email", w.SupportEmail, b.SupportEmail)
	testutil.Equal(t, "dir", w.Dir(), "/fleet")
	testutil.Equal(t, "welcome", len(w.Welcome), 2)
}
