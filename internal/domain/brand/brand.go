// Package brand holds the pure rules behind a fleet's own look in the web
// UI: reading a primary color, choosing text that stays readable on it
// (WCAG 2.2 AA), deriving the variants the light and dark themes need, and
// recognising the image types a logo or a favicon may be.
package brand

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"
)

// DefaultName is the name the UI shows when a fleet sets none.
const DefaultName = "wpfleet"

// DefaultPrimaryColor is the primary color when a fleet sets none: a
// neutral blue that carries white text at more than 6:1.
const DefaultPrimaryColor = "#1f5fbf"

// MaxImageSize is the largest logo or favicon the server serves: 512 KiB.
const MaxImageSize = 512 << 10

// The contrast ratios of WCAG 2.2 level AA.
const (
	// TextContrast is the least contrast of normal text (1.4.3).
	TextContrast = 4.5
	// UIContrast is the least contrast of a control's edge against what is
	// around it (1.4.11).
	UIContrast = 3.0
)

// ErrInvalidColor is returned for a color that is not a CSS hex color.
var ErrInvalidColor = errors.New("not a CSS hex color like #1f5fbf or #15b")

// Color is an sRGB color with 8 bits per channel, as 0xRRGGBB.
type Color uint32

// The colors the stylesheet's themes are built on; app.css holds the same
// values, and a test keeps the two equal.
const (
	// Black is one of the two text colors a primary color can carry.
	Black Color = 0x000000
	// White is the other.
	White Color = 0xffffff
	// LightBackground is the light theme's page.
	LightBackground Color = 0xf6f7f9
	// LightSurface is the light theme's card.
	LightSurface Color = 0xffffff
	// DarkBackground is the dark theme's page.
	DarkBackground Color = 0x121417
	// DarkSurface is the dark theme's card.
	DarkSurface Color = 0x1b1e23
)

// The bit offsets of the red and green channels in a Color.
const (
	redShift   = 16
	greenShift = 8
)

// RGB makes a color from its channels.
func RGB(r, g, b uint8) Color {
	return Color(r)<<redShift | Color(g)<<greenShift | Color(b)
}

// R is the red channel.
func (c Color) R() uint8 { return uint8(c >> redShift) } //nolint:gosec // G115: the truncation keeps the channel

// G is the green channel.
func (c Color) G() uint8 { return uint8(c >> greenShift) } //nolint:gosec // G115: the truncation keeps the channel

// B is the blue channel.
func (c Color) B() uint8 { return uint8(c) } //nolint:gosec // G115: the truncation keeps the channel

// ParseColor reads a CSS hex color: #rgb or #rrggbb, in either case.
func ParseColor(s string) (Color, error) {
	hex, ok := strings.CutPrefix(s, "#")
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrInvalidColor, s)
	}

	if len(hex) == len("rgb") {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}

	if len(hex) != len("rrggbb") {
		return 0, fmt.Errorf("%w: %q", ErrInvalidColor, s)
	}

	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidColor, s)
	}

	return Color(n), nil
}

// String writes the color as #rrggbb.
func (c Color) String() string {
	return fmt.Sprintf("#%06x", uint32(c))
}

// Luminance is the relative luminance of WCAG 2.2, from 0 for black to 1
// for white.
func (c Color) Luminance() float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255 //nolint:mnd // the largest value of a channel

		if s <= 0.04045 { //nolint:mnd // the sRGB transfer function's knee
			return s / 12.92 //nolint:mnd // the sRGB transfer function's linear slope
		}

		return math.Pow((s+0.055)/1.055, 2.4) //nolint:mnd // the sRGB transfer function
	}

	return 0.2126*channel(c.R()) + 0.7152*channel(c.G()) + 0.0722*channel(c.B())
}

// Contrast is the WCAG contrast ratio of two colors, from 1 to 21.
func Contrast(a, b Color) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05) //nolint:mnd // the flare term of WCAG's ratio
}

// TextOn is the text color that reads best on c: black or white. One of
// the two always reaches at least 4.58:1, so the text passes AA on any c.
func TextOn(c Color) Color {
	if Contrast(c, Black) >= Contrast(c, White) {
		return Black
	}

	return White
}

// mix moves c toward target by t, from 0 (c) to 1 (target).
func mix(c, target Color, t float64) Color {
	ch := func(a, b uint8) uint8 {
		return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t))
	}

	return RGB(ch(c.R(), target.R()), ch(c.G(), target.G()), ch(c.B(), target.B()))
}

// mixSteps is how finely Against looks for a passing shade.
const mixSteps = 50

// Against moves c in the direction of target (black or white) by the
// smallest step that gives it at least ratio against every color of on,
// and returns c itself when it already has it. Toward black or white the
// contrast with a light or dark theme only grows, so the result always
// passes when target is the opposite of the theme.
func Against(c Color, ratio float64, target Color, on ...Color) Color {
	passes := func(x Color) bool {
		for _, o := range on {
			if Contrast(x, o) < ratio {
				return false
			}
		}

		return true
	}

	for i := range mixSteps + 1 {
		x := mix(c, target, float64(i)/mixSteps)
		if passes(x) {
			return x
		}
	}

	return target
}

// Shades are a primary color's variants in one theme.
type Shades struct {
	// Primary fills buttons; its edge stands out from the page (3:1).
	Primary Color
	// Contrast is the text on Primary (4.5:1).
	Contrast Color
	// Link is the primary color as text on the page and on cards (4.5:1).
	Link Color
}

// Theme is a primary color's shades in the light and the dark theme.
type Theme struct {
	// Light and Dark are the shades of each theme.
	Light, Dark Shades
}

// NewTheme derives the shades of both themes from a primary color: as it
// is where it already passes, darker on the light theme or lighter on the
// dark one where it does not.
func NewTheme(c Color) Theme {
	shades := func(toward Color, on ...Color) Shades {
		primary := Against(c, UIContrast, toward, on...)

		return Shades{Primary: primary, Contrast: TextOn(primary), Link: Against(c, TextContrast, toward, on...)}
	}

	return Theme{
		Light: shades(Black, LightBackground, LightSurface),
		Dark:  shades(White, DarkBackground, DarkSurface),
	}
}

// CSS writes the theme as custom properties for the stylesheet: the light
// shades on :root and the dark ones under prefers-color-scheme.
func (t Theme) CSS() string {
	props := func(s Shades) string {
		return fmt.Sprintf("--wf-primary:%s;--wf-primary-contrast:%s;--wf-link:%s;", s.Primary, s.Contrast, s.Link)
	}

	return ":root{" + props(t.Light) + "}\n" +
		"@media (prefers-color-scheme: dark){:root{" + props(t.Dark) + "}}\n"
}

// MediaType is the type of an image the server serves for a brand.
type MediaType string

// The image types a logo or favicon may be.
const (
	// PNG is a PNG image.
	PNG MediaType = "image/png"
	// SVG is an SVG image, served only for <img>, where it runs no script.
	SVG MediaType = "image/svg+xml"
	// WebP is a WebP image.
	WebP MediaType = "image/webp"
)

// Errors of DetectImage.
var (
	// ErrImageType is a file whose extension is not .png, .svg or .webp.
	ErrImageType = errors.New("must be a .png, .svg or .webp file")
	// ErrImageContent is a file whose content does not match its extension.
	ErrImageContent = errors.New("the content does not match the file extension")
	// ErrImageSize is an empty file or one larger than MaxImageSize.
	ErrImageSize = errors.New("must be between 1 byte and 512 KiB")
)

// TypeOf gives the media type a file name's extension allows, or
// ErrImageType.
func TypeOf(name string) (MediaType, error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return PNG, nil
	case ".svg":
		return SVG, nil
	case ".webp":
		return WebP, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrImageType, name)
	}
}

// svgSniffLength is how much of a file DetectImage searches for <svg.
const svgSniffLength = 1024

// DetectImage checks an image's size and that its content is what its file
// name's extension says, and returns its media type.
func DetectImage(name string, data []byte) (MediaType, error) {
	t, err := TypeOf(name)
	if err != nil {
		return "", err
	}

	if len(data) == 0 || len(data) > MaxImageSize {
		return "", fmt.Errorf("%w: %q has %d bytes", ErrImageSize, name, len(data))
	}

	var ok bool

	switch t {
	case PNG:
		ok = bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case WebP:
		ok = len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
	case SVG:
		head := data[:min(len(data), svgSniffLength)]
		ok = bytes.Contains(bytes.ToLower(head), []byte("<svg"))
	}

	if !ok {
		return "", fmt.Errorf("%w: %q is not %s", ErrImageContent, name, t)
	}

	return t, nil
}
