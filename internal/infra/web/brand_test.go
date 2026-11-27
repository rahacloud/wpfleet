package web_test

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/brand"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// svgPolicy is the Content-Security-Policy of an SVG brand image.
const svgPolicy = "default-src 'none'; style-src 'unsafe-inline'"

// northwind is the invented brand of testdata/northwind, read from dir.
func northwind(dir string) web.Branding {
	return web.Branding{
		Name: "Northwind Media", Logo: "brand/logo.svg", Favicon: "brand/favicon.png",
		Dir: func() string { return dir }, PrimaryColor: "#0e7c66",
		SupportURL: "", SupportEmail: "web-help@example.com",
		Welcome: map[i18n.Lang]string{i18n.English: "Open your site's dashboard from here.", i18n.Persian: ""},
	}
}

// fleetWithBrand is a fleet directory holding the test brand's files under
// brand/.
func fleetWithBrand(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	testutil.NoError(t, os.MkdirAll(filepath.Join(dir, "brand"), 0o750))

	for _, name := range []string{"logo.svg", "favicon.png", "not-a-logo.png"} {
		data, err := os.ReadFile(filepath.Join("testdata", "northwind", name)) //nolint:gosec // G304: fixed names
		testutil.NoError(t, err)
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, "brand", name), data, 0o600)) //nolint:gosec // G703: fixed names
	}

	return dir
}

// brandHarness is a server with the test brand.
func brandHarness(t *testing.T) (*harness, string) {
	t.Helper()

	dir := fleetWithBrand(t)

	return newHarnessWith(t, func(d *web.Deps) { d.Branding = northwind(dir) }), dir
}

// The home page greets people with the brand's welcome text, and the
// pages send them to the brand's support.
func TestWelcomeAndSupportFromTheBrand(t *testing.T) {
	t.Parallel()

	h, _ := brandHarness(t)
	cookie, _ := h.as(editor(), "")

	home := h.request(http.MethodGet, "/", nil, cookie).Body.String()
	testutil.Contains(t, "welcome", home, `<p class="wf-page-welcome">Open your site&#39;s dashboard from here.</p>`)

	refusal := h.request(http.MethodGet, "/sites/production/blog", nil, cookie).Body.String()
	testutil.Contains(t, "refusal", refusal, "ask your support team for access", `href="mailto:web-help@example.com"`)
}

// checkFooter checks the footer that no branding changes.
func checkFooter(t *testing.T, what, body string) {
	t.Helper()

	testutil.Contains(t, what+": footer", body,
		`<footer class="wf-footer">`,
		`href="`+ui.Repository+`"`,
		`href="`+ui.LicenseURL+`"`,
		">github.com/rahacloud/wpfleet</a>",
		"v0.0.0-test",
	)
}

func TestSignInPage(t *testing.T) {
	t.Parallel()

	h, _ := brandHarness(t)

	w := h.request(http.MethodGet, "/auth/signin?return="+url.QueryEscape("/sites/staging/blog"), nil, nil)
	testutil.Equal(t, "status", w.Code, http.StatusOK)

	body := w.Body.String()
	testutil.Contains(t, "sign-in page", body,
		"Welcome to Northwind Media",
		"Sign in with your company account",
		`href="/auth/login?return=%2Fsites%2Fstaging%2Fblog"`,
		`<img class="wf-signin-logo" src="/brand/logo?v=`,
		`<title>Sign in · Northwind Media</title>`,
		`href="mailto:web-help@example.com"`,
		"Need help?",
	)
	checkFooter(t, "sign-in", body)

	// The page links itself in Persian, since there is no session to keep a
	// choice in, and the link works.
	testutil.Contains(t, "language link", body, `href="/auth/signin?lang=fa&amp;return=%2Fsites%2Fstaging%2Fblog"`)

	w = h.request(http.MethodGet, "/auth/signin?lang=fa&return=%2F", nil, nil)
	testutil.Contains(t, "in Persian", w.Body.String(), `lang="fa" dir="rtl"`, "ورود با حساب کاری شرکت", "مجوز Apache-2.0")

	// A return path to another site is dropped.
	w = h.request(http.MethodGet, "/auth/signin?return="+url.QueryEscape("//evil.example.com/"), nil, nil)
	testutil.Contains(t, "safe return", w.Body.String(), `href="/auth/login?return=%2F"`)

	// A person who is signed in goes on.
	cookie, _ := h.as(editor(), "")
	w = h.request(http.MethodGet, "/auth/signin?return=/sites/staging/blog", nil, cookie)
	testutil.Equal(t, "signed in", w.Code, http.StatusSeeOther)
	testutil.Equal(t, "goes on", w.Header().Get("Location"), "/sites/staging/blog")
}

func TestFooterOnEveryPage(t *testing.T) {
	t.Parallel()

	for name, h := range map[string]*harness{"wpfleet": newHarness(t), "branded": func() *harness {
		h, _ := brandHarness(t)

		return h
	}()} {
		cookie, csrf := h.as(editor(), "")
		stranger, _ := h.as(stranger(), "fa")

		pages := map[string]func() string{
			"sign-in": func() string { return h.request(http.MethodGet, "/auth/signin", nil, nil).Body.String() },
			"fleet":   func() string { return h.request(http.MethodGet, "/", nil, cookie).Body.String() },
			"site":    func() string { return h.request(http.MethodGet, "/sites/staging/blog", nil, cookie).Body.String() },
			"refusal": func() string {
				return h.request(http.MethodGet, "/sites/production/blog", nil, cookie).Body.String()
			},
			"no access":  func() string { return h.request(http.MethodGet, "/", nil, stranger).Body.String() },
			"not found":  func() string { return h.request(http.MethodGet, "/nowhere", nil, cookie).Body.String() },
			"signed out": func() string { return h.request(http.MethodPost, "/auth/logout", form(csrf), cookie).Body.String() },
		}

		for page, get := range pages {
			checkFooter(t, name+" "+page, get())
		}
	}
}

func TestHeader(t *testing.T) {
	t.Parallel()

	h, _ := brandHarness(t)
	cookie, _ := h.as(editor(), "")

	body := h.request(http.MethodGet, "/", nil, cookie).Body.String()
	testutil.Contains(t, "header", body,
		`<img class="wf-brand-logo" src="/brand/logo?v=`,
		`<span class="wf-brand-name">Northwind Media</span>`,
		`<span class="wf-user" title="editor@example.com">An Editor</span>`,
		`name="lang" value="fa"`,
		`action="/auth/logout"`,
		`<link rel="stylesheet" href="/brand/theme.css?v=`,
		`<link rel="icon" href="/brand/favicon?v=`,
		`type="image/png"`,
	)
	testutil.NotContains(t, "no separate submit for the language", body, ">Change<")
	testutil.NotContains(t, "no language links with a session", body, "?lang=fa")

	// Without a logo the name stands alone.
	plain := newHarness(t)
	cookie, _ = plain.as(editor(), "")
	body = plain.request(http.MethodGet, "/", nil, cookie).Body.String()
	testutil.NotContains(t, "no logo", body, "wf-brand-logo")
	testutil.Contains(t, "wpfleet's name and favicon", body,
		`<span class="wf-brand-name">wpfleet</span>`, `type="image/svg+xml"`)
}

func TestServeLogo(t *testing.T) {
	t.Parallel()

	h, dir := brandHarness(t)

	logo, err := web.LoadBrandImage(dir, "brand/logo.svg")
	testutil.NoError(t, err)

	// No session is needed: the sign-in page shows the logo.
	w := h.request(http.MethodGet, "/brand/logo?v="+logo.Version, nil, nil)
	testutil.Equal(t, "status", w.Code, http.StatusOK)
	testutil.Equal(t, "type", w.Header().Get("Content-Type"), "image/svg+xml")
	testutil.Equal(t, "svg policy", w.Header().Get("Content-Security-Policy"), svgPolicy)
	testutil.Equal(t, "etag", w.Header().Get("ETag"), `"`+logo.Version+`"`)
	testutil.Equal(t, "cached for a year", w.Header().Get("Cache-Control"), "public, max-age=31536000, immutable")
	testutil.Equal(t, "body", w.Body.String(), string(logo.Data))

	w = h.request(http.MethodGet, "/brand/logo", nil, nil, "If-None-Match", `W/"other", "`+logo.Version+`"`)
	testutil.Equal(t, "not modified", w.Code, http.StatusNotModified)
	testutil.Equal(t, "an old version is checked again", w.Header().Get("Cache-Control"), "public, max-age=3600")

	w = h.request(http.MethodGet, "/brand/favicon", nil, nil)
	testutil.Equal(t, "favicon", w.Header().Get("Content-Type"), "image/png")
	testutil.True(t, "no svg policy on a png", w.Header().Get("Content-Security-Policy") != svgPolicy)

	w = h.request(http.MethodGet, "/brand/theme.css", nil, nil)
	testutil.Contains(t, "theme", w.Header().Get("Content-Type"), "text/css")
	testutil.Contains(t, "theme", w.Body.String(), "--wf-primary:#0e7c66", "--wf-primary-contrast:#ffffff")

	// wpfleet's own look has no logo and its own favicon.
	plain := newHarness(t)
	testutil.Equal(t, "no logo", plain.request(http.MethodGet, "/brand/logo", nil, nil).Code, http.StatusNotFound)

	w = plain.request(http.MethodGet, "/favicon.ico", nil, nil)
	testutil.Equal(t, "default favicon", w.Code, http.StatusOK)
	testutil.Equal(t, "an svg", w.Header().Get("Content-Type"), "image/svg+xml")
	testutil.Contains(t, "default theme", plain.request(http.MethodGet, "/brand/theme.css", nil, nil).Body.String(),
		"--wf-primary:"+brand.DefaultPrimaryColor)
}

func TestBrokenLogoIsLeftOut(t *testing.T) {
	t.Parallel()

	dir := fleetWithBrand(t)
	h := newHarnessWith(t, func(d *web.Deps) {
		b := northwind(dir)
		b.Logo = "brand/not-a-logo.png"
		b.Favicon = "brand/missing.png"
		d.Branding = b
	})

	testutil.Equal(t, "no logo", h.request(http.MethodGet, "/brand/logo", nil, nil).Code, http.StatusNotFound)
	w := h.request(http.MethodGet, "/brand/favicon", nil, nil)
	testutil.Equal(t, "default favicon", w.Header().Get("Content-Type"), "image/svg+xml")

	body := h.request(http.MethodGet, "/auth/signin", nil, nil).Body.String()
	testutil.NotContains(t, "page without the logo", body, "/brand/logo")
	testutil.Contains(t, "the name still shows", body, "Northwind Media")
}

func TestBadPrimaryColorIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	//nolint:exhaustruct_v5 // the color is checked before the rest is used
	_, err := web.New(web.Deps{
		Auth: h.auth, Sessions: h.sessions, Access: fakeAccess{}, Repo: h.repo, Ops: h.ops, Locks: h.locks,
		Audit: h.audit.write, Branding: web.Branding{PrimaryColor: "blue"},
	})
	testutil.ErrorIs(t, err, brand.ErrInvalidColor)
}

func TestLoadBrandImage(t *testing.T) {
	t.Parallel()

	dir := fleetWithBrand(t)

	img, err := web.LoadBrandImage(dir, "brand/favicon.png")
	testutil.NoError(t, err)
	testutil.Equal(t, "type", img.Type, brand.PNG)
	testutil.Equal(t, "version", len(img.Version), 16)

	testutil.NoError(t, os.Symlink("/etc/hostname", filepath.Join(dir, "brand", "escape.png")))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "brand", "huge.png"),
		append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, brand.MaxImageSize)...), 0o600))
	testutil.NoError(t, os.Mkdir(filepath.Join(dir, "brand", "dir.png"), 0o750))

	for name, want := range map[string]error{
		"brand/not-a-logo.png": brand.ErrImageContent,
		"brand/huge.png":       brand.ErrImageSize,
		"brand/missing.png":    os.ErrNotExist,
		"brand/escape.png":     nil,
		"../outside.png":       nil,
		"brand/dir.png":        nil,
	} {
		_, err := web.LoadBrandImage(dir, name)
		testutil.ErrorIs(t, err, web.ErrBrandImage)

		if want != nil && !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", name, err, want)
		}

		if strings.Contains(err.Error(), "%!") {
			t.Errorf("%s: a bad format verb: %v", name, err)
		}
	}
}
