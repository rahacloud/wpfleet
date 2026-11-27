package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/brand"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// Branding is the fleet's own look in the UI: its name, logo, favicon and
// color, where to ask for help, and a welcome on the home page. The zero
// value is wpfleet's own look. The footer is not part of it: every page
// names wpfleet, its version, its license and its repository.
type Branding struct {
	// Name is shown beside the logo and in the title; empty is "wpfleet".
	Name string
	// Logo and Favicon are PNG, SVG or WebP files inside the fleet
	// repository, read from Dir; empty Logo shows the name alone and empty
	// Favicon wpfleet's own.
	Logo, Favicon string
	// LogoIncludesName says the logo is a wordmark that shows the name, so
	// the header keeps the name for screen readers only. Without a logo
	// that can be shown, the name shows as usual.
	LogoIncludesName bool
	// Dir is the fleet directory the image paths are read from, the newest
	// snapshot of the clone; nil reads no image.
	Dir func() string
	// PrimaryColor is a CSS hex color; empty is the default blue.
	PrimaryColor string
	// SupportURL and SupportEmail are where "Need help?" leads, the URL
	// first; no link when both are empty.
	SupportURL, SupportEmail string
	// Welcome is a short text on the home page per language; a language
	// without one shows the English text.
	Welcome map[i18n.Lang]string
}

// The paths the brand's files are served at. They need no session: the
// sign-in page shows them.
const (
	logoPath    = "/brand/logo"
	faviconPath = "/brand/favicon"
	themePath   = "/brand/theme.css"
)

// The cache lifetimes of the brand's files. A page links each file with
// its version in the query, so a request with the current version may be
// kept for a year; one without it, like /favicon.ico, is checked again
// after an hour.
const (
	cacheVersioned   = "public, max-age=31536000, immutable"
	cacheUnversioned = "public, max-age=3600"
)

// svgPolicy is the Content-Security-Policy of an SVG logo or favicon. The
// UI shows them only in <img>, where they run no script; this keeps one
// opened on its own address from running any either.
const svgPolicy = "default-src 'none'; style-src 'unsafe-inline'"

// BrandImage is a logo or favicon read and checked.
type BrandImage struct {
	// Type is the image's media type.
	Type brand.MediaType
	// Data is the file.
	Data []byte
	// Version is a hash of Data; it is the ETag and the query that
	// versions the image's URL.
	Version string
}

// ErrBrandImage wraps every reason LoadBrandImage refuses a file.
var ErrBrandImage = errors.New("the brand image cannot be used")

// LoadBrandImage reads a logo or favicon at rel inside dir and checks it:
// it must stay inside dir (symbolic links included), be a PNG, SVG or WebP
// file whose content matches its extension, and be at most 512 KiB.
func LoadBrandImage(dir, rel string) (BrandImage, error) {
	f, err := os.OpenInRoot(dir, rel)
	if err != nil {
		return BrandImage{}, fmt.Errorf("%w: %w", ErrBrandImage, err)
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return BrandImage{}, fmt.Errorf("%w: %w", ErrBrandImage, err)
	}

	if !info.Mode().IsRegular() {
		return BrandImage{}, fmt.Errorf("%w: %q is not a regular file", ErrBrandImage, rel)
	}

	data, err := io.ReadAll(io.LimitReader(f, brand.MaxImageSize+1))
	if err != nil {
		return BrandImage{}, fmt.Errorf("%w: reading %q: %w", ErrBrandImage, rel, err)
	}

	t, err := brand.DetectImage(rel, data)
	if err != nil {
		return BrandImage{}, fmt.Errorf("%w: %w", ErrBrandImage, err)
	}

	return BrandImage{Type: t, Data: data, Version: version(data)}, nil
}

// versionLength is how many hex digits of a SHA-256 version a file.
const versionLength = 16

// version is the short hash that versions a file's URL.
func version(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])[:versionLength]
}

// brandAssets serves the brand's files and describes them to the pages.
// The theme and the default favicon are fixed; the logo and the favicon
// are read again when the snapshot changes, so a push changes them.
type brandAssets struct {
	b              Branding
	theme          BrandImage
	defaultFavicon BrandImage
	log            *slog.Logger

	mu      sync.Mutex
	dir     string
	logo    *BrandImage
	favicon *BrandImage
}

// newBrandAssets fills in the defaults and builds the theme's stylesheet.
func newBrandAssets(b Branding, log *slog.Logger) (*brandAssets, error) {
	if strings.TrimSpace(b.Name) == "" {
		b.Name = brand.DefaultName
	}

	if b.PrimaryColor == "" {
		b.PrimaryColor = brand.DefaultPrimaryColor
	}

	color, err := brand.ParseColor(b.PrimaryColor)
	if err != nil {
		return nil, fmt.Errorf("the brand's primary color: %w", err)
	}

	css := []byte(brand.NewTheme(color).CSS())

	favicon, err := fs.ReadFile(ui.Static(), ui.FaviconFile)
	if err != nil {
		return nil, fmt.Errorf("reading the default favicon: %w", err)
	}

	return &brandAssets{
		b:              b,
		theme:          BrandImage{Type: "text/css", Data: css, Version: version(css)},
		defaultFavicon: BrandImage{Type: brand.SVG, Data: favicon, Version: version(favicon)},
		log:            log,
		mu:             sync.Mutex{},
		dir:            "",
		logo:           nil,
		favicon:        nil,
	}, nil
}

// images returns the logo, nil when there is none, and the favicon, read
// again when the fleet directory changed. A file that cannot be used is
// logged once per directory and left out, so a bad push never breaks the
// pages; serve checks the files at start.
func (a *brandAssets) images() (*BrandImage, *BrandImage) {
	if a.b.Dir == nil {
		return nil, &a.defaultFavicon
	}

	dir := a.b.Dir()

	a.mu.Lock()
	defer a.mu.Unlock()

	if dir != a.dir {
		a.dir = dir
		a.logo = a.load(dir, "logo", a.b.Logo)
		a.favicon = a.load(dir, "favicon", a.b.Favicon)
	}

	if a.favicon == nil {
		return a.logo, &a.defaultFavicon
	}

	return a.logo, a.favicon
}

// load reads one image, or logs why it cannot be used.
func (a *brandAssets) load(dir, what, rel string) *BrandImage {
	if rel == "" || dir == "" {
		return nil
	}

	img, err := LoadBrandImage(dir, rel)
	if err != nil {
		a.log.Warn("the brand's "+what+" is left out", "path", rel, "error", err)

		return nil
	}

	return &img
}

// view describes the brand to the layout, in the language l.
func (a *brandAssets) view(l i18n.Lang) ui.Brand {
	logo, favicon := a.images()

	v := ui.Brand{
		Name:             a.b.Name,
		LogoURL:          "",
		LogoIncludesName: false,
		FaviconURL:       faviconPath + "?v=" + favicon.Version,
		FaviconType:      string(favicon.Type),
		ThemeURL:         themePath + "?v=" + a.theme.Version,
		SupportURL:       a.b.SupportURL,
		Welcome:          a.b.Welcome[l],
		WelcomeLang:      l,
	}

	if logo != nil {
		v.LogoURL = logoPath + "?v=" + logo.Version
		v.LogoIncludesName = a.b.LogoIncludesName
	}

	if v.SupportURL == "" && a.b.SupportEmail != "" {
		v.SupportURL = "mailto:" + url.PathEscape(a.b.SupportEmail)
	}

	if v.Welcome == "" {
		v.Welcome, v.WelcomeLang = a.b.Welcome[i18n.English], i18n.English
	}

	return v
}

// serveLogo serves the logo, or 404 when there is none.
func (s *Server) serveLogo(c *echo.Context) error {
	logo, _ := s.brand.images()
	if logo == nil {
		return echo.ErrNotFound
	}

	return serveImage(c, *logo)
}

// serveFavicon serves the favicon, wpfleet's own when the fleet sets none.
func (s *Server) serveFavicon(c *echo.Context) error {
	_, favicon := s.brand.images()

	return serveImage(c, *favicon)
}

// serveTheme serves the stylesheet with the brand's colors.
func (s *Server) serveTheme(c *echo.Context) error {
	return serveImage(c, s.brand.theme)
}

// serveImage serves one brand file with its ETag, cached for a year when
// the request names its current version, and answers 304 to a browser that
// already has it.
func serveImage(c *echo.Context, img BrandImage) error {
	etag := `"` + img.Version + `"`
	h := c.Response().Header()
	h.Set("ETag", etag)

	if c.QueryParam("v") == img.Version {
		h.Set("Cache-Control", cacheVersioned)
	} else {
		h.Set("Cache-Control", cacheUnversioned)
	}

	if img.Type == brand.SVG {
		h.Set("Content-Security-Policy", svgPolicy)
	}

	if etagMatches(c.Request().Header.Get("If-None-Match"), etag) {
		return c.NoContent(http.StatusNotModified)
	}

	return c.Blob(http.StatusOK, string(img.Type), img.Data)
}

// etagMatches reports whether an If-None-Match header names etag, weakly
// or not, or is *.
func etagMatches(header, etag string) bool {
	for candidate := range strings.SplitSeq(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == etag || candidate == "*" {
			return true
		}
	}

	return false
}
