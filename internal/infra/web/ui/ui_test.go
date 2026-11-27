package ui_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/brand"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// stylesheet is the embedded app.css.
func stylesheet(t *testing.T) string {
	t.Helper()

	css, err := fs.ReadFile(ui.Static(), "app.css")
	testutil.NoError(t, err)

	return string(css)
}

// The custom properties of the page and the card colors.
//
//nolint:gosec // G101: CSS custom property names, not credentials
const (
	bgToken      = "--wf-bg"
	surfaceToken = "--wf-surface"
)

// TestDesignSystemContract checks that the stylesheet defines every class
// and token the page components may use.
func TestDesignSystemContract(t *testing.T) {
	t.Parallel()

	css := stylesheet(t)

	for _, class := range []string{
		"wf-btn", "wf-btn-primary", "wf-btn-secondary", "wf-btn-danger", "wf-card", "wf-grid", "wf-badge",
		"wf-badge-ok", "wf-badge-warn", "wf-badge-bad", "wf-muted", "wf-help", "wf-stack", "wf-row",
	} {
		if !regexp.MustCompile(`\.` + class + `[\s,{:>]`).MatchString(css) {
			t.Errorf("app.css has no rule for .%s", class)
		}
	}

	for _, token := range []string{
		"--wf-primary", "--wf-primary-contrast", bgToken, surfaceToken, "--wf-text", "--wf-muted", "--wf-radius",
	} {
		testutil.Contains(t, "token", css, token+":")
	}

	// Right to left comes from logical properties only.
	for _, physical := range []string{"margin-left", "margin-right", "padding-left", "padding-right", "float:", "right:"} {
		testutil.NotContains(t, "logical properties", css, physical)
	}
}

// TestThemeColorsMatchTheDomain keeps the page and card colors of app.css
// equal to the ones the brand's shades are checked against.
func TestThemeColorsMatchTheDomain(t *testing.T) {
	t.Parallel()

	css := stylesheet(t)
	light, dark, ok := strings.Cut(css, "@media (prefers-color-scheme: dark)")
	testutil.True(t, "a dark theme", ok)

	for _, c := range []struct {
		part, token string
		want        brand.Color
	}{
		{light, bgToken, brand.LightBackground},
		{light, surfaceToken, brand.LightSurface},
		{dark, bgToken, brand.DarkBackground},
		{dark, surfaceToken, brand.DarkSurface},
	} {
		testutil.Contains(t, c.token, c.part, c.token+": "+c.want.String()+";")
	}

	testutil.Contains(t, "default primary", light, "--wf-primary: "+brand.DefaultPrimaryColor+";")
}

// TestControlEdgesContrast keeps the edges of the controls at 3:1 at least
// on the page and the card in both themes (WCAG 1.4.11): they use
// --wf-muted as it is, never mixed lighter, and --wf-muted reaches 3:1.
func TestControlEdgesContrast(t *testing.T) {
	t.Parallel()

	css := stylesheet(t)
	testutil.NotContains(t, "a control edge mixed lighter", css, "color-mix(in srgb, var(--wf-muted) 50%")

	secondary := regexp.MustCompile(`(?s)\.wf-btn-secondary \{[^}]*\}`).FindString(css)
	testutil.Contains(t, "secondary button", secondary, "border-color: var(--wf-muted);")

	light, dark, ok := strings.Cut(css, "@media (prefers-color-scheme: dark)")
	testutil.True(t, "a dark theme", ok)

	muted := regexp.MustCompile(`--wf-muted: (#[0-9a-f]{6});`)

	for _, theme := range []struct {
		name, part  string
		backgrounds []brand.Color
	}{
		{"light", light, []brand.Color{brand.LightBackground, brand.LightSurface}},
		{"dark", dark, []brand.Color{brand.DarkBackground, brand.DarkSurface}},
	} {
		m := muted.FindStringSubmatch(theme.part)
		testutil.True(t, theme.name+" --wf-muted", m != nil)

		edge, err := brand.ParseColor(m[1])
		testutil.NoError(t, err)

		for _, bg := range theme.backgrounds {
			ratio := brand.Contrast(edge, bg)
			testutil.True(t, theme.name+" edge on "+bg.String()+" at 3:1", ratio >= 3)
		}
	}
}

// TestFooterIgnoresBranding renders the layout with a brand that tries to
// change the footer: the footer stays wpfleet's.
func TestFooterIgnoresBranding(t *testing.T) {
	t.Parallel()

	for _, l := range i18n.Supported() {
		var b strings.Builder

		p := ui.Page{
			L: l, Title: "Sites", User: "", UserEmail: "", CSRF: "", Return: "/", LangLinks: nil, Version: "v1.2.3",
			Brand: ui.Brand{
				Name: `Northwind <footer>Powered by someone else</footer>`, LogoURL: "/brand/logo?v=1",
				FaviconURL: "/brand/favicon?v=1", FaviconType: "image/png", ThemeURL: "/brand/theme.css?v=1",
				SupportURL: "https://help.example.com/", Welcome: "",
			},
		}

		m := ui.Message{
			Title: "t", Text: "x", Hint: "", Bad: false, Link: "", LinkText: "", Support: ui.Support{Email: "", URL: ""},
		}
		err := ui.MessagePage(p, m).Render(t.Context(), &b)
		testutil.NoError(t, err)

		html := b.String()
		testutil.Contains(t, string(l)+" footer", html,
			l.T(i18n.PoweredBy)+` <a href="https://github.com/rahacloud/wpfleet">wpfleet</a> <span class="ltr">v1.2.3</span>`,
			`<a href="https://github.com/rahacloud/wpfleet/blob/main/LICENSE">`+l.T(i18n.LicenseName)+`</a>`,
			">github.com/rahacloud/wpfleet</a>",
			`href="https://help.example.com/"`,
		)
		testutil.NotContains(t, "the name is escaped", html, "<footer>Powered by someone else")
		testutil.Equal(t, "one footer", strings.Count(html, "<footer"), 1)
	}
}
