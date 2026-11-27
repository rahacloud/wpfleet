package web

import (
	"log/slog"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func TestBrandView(t *testing.T) {
	t.Parallel()

	a, err := newBrandAssets(Branding{ //nolint:exhaustruct_v5 // only what the view reads
		Name: " ", SupportEmail: "web-help@example.com",
		Welcome: map[i18n.Lang]string{i18n.English: "Hello.", i18n.Persian: "سلام."},
	}, slog.New(slog.DiscardHandler))
	testutil.NoError(t, err)

	v := a.view(i18n.Persian)
	testutil.Equal(t, "default name", v.Name, "wpfleet")
	testutil.Equal(t, "mailto", v.SupportURL, "mailto:web-help@example.com")
	testutil.Equal(t, "welcome", v.Welcome, "سلام.")
	testutil.Equal(t, "no logo", v.LogoURL, "")
	testutil.Equal(t, "no wordmark", v.LogoIncludesName, false)
	testutil.Equal(t, "default favicon", v.FaviconType, "image/svg+xml")

	// The flag alone does not hide the name: without a logo there is
	// nothing that shows it.
	a.b.LogoIncludesName = true
	testutil.Equal(t, "no wordmark without a logo", a.view(i18n.English).LogoIncludesName, false)

	a.b.Welcome = map[i18n.Lang]string{i18n.English: "Hello."}
	testutil.Equal(t, "English when Persian has none", a.view(i18n.Persian).Welcome, "Hello.")

	a.b.SupportURL = "https://help.example.com/"
	testutil.Equal(t, "the URL first", a.view(i18n.English).SupportURL, "https://help.example.com/")
}
