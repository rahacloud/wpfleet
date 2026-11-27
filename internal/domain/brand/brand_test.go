package brand_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/brand"
)

func TestParseColor(t *testing.T) {
	t.Parallel()

	good := map[string]string{
		brand.DefaultPrimaryColor: brand.DefaultPrimaryColor,
		"#1F5FBF":                 brand.DefaultPrimaryColor,
		"#15b":                    "#1155bb",
		"#000":                    "#000000",
	}

	for in, want := range good {
		c, err := brand.ParseColor(in)
		if err != nil || c.String() != want {
			t.Errorf("ParseColor(%q) = %s, %v; want %s", in, c, err, want)
		}
	}

	for _, in := range []string{"", "1f5fbf", "#1f5fb", "#1f5fbfa", "#ggg", "blue", "#+1234", "rgb(1,2,3)", "#1f5fbf "} {
		_, err := brand.ParseColor(in)
		if !errors.Is(err, brand.ErrInvalidColor) {
			t.Errorf("ParseColor(%q) = %v, want ErrInvalidColor", in, err)
		}
	}
}

func TestContrast(t *testing.T) {
	t.Parallel()

	if got := brand.Contrast(brand.Black, brand.White); math.Abs(got-21) > 0.01 {
		t.Errorf("black on white = %.2f, want 21", got)
	}

	if got := brand.Contrast(brand.White, brand.White); got != 1 {
		t.Errorf("white on white = %.2f, want 1", got)
	}

	// #767676 on white is the well-known edge of AA: 4.54:1.
	grey, _ := brand.ParseColor("#767676")
	if got := brand.Contrast(grey, brand.White); math.Abs(got-4.54) > 0.01 {
		t.Errorf("#767676 on white = %.2f, want 4.54", got)
	}
}

func TestTextOnAlwaysPassesAA(t *testing.T) {
	t.Parallel()

	for r := 0; r < 256; r += 15 {
		for g := 0; g < 256; g += 15 {
			for b := 0; b < 256; b += 15 {
				c := brand.RGB(uint8(r), uint8(g), uint8(b))
				if got := brand.Contrast(c, brand.TextOn(c)); got < brand.TextContrast {
					t.Fatalf("text on %s has %.2f:1", c, got)
				}
			}
		}
	}
}

func TestTextOnPicks(t *testing.T) {
	t.Parallel()

	tests := map[string]brand.Color{
		brand.DefaultPrimaryColor: brand.White,
		"#ffd400":                 brand.Black, // a bright yellow
		"#0b1f3a":                 brand.White, // a navy
		"#7fdbff":                 brand.Black, // a light aqua
	}

	for hex, want := range tests {
		c, _ := brand.ParseColor(hex)
		if got := brand.TextOn(c); got != want {
			t.Errorf("TextOn(%s) = %s, want %s", hex, got, want)
		}
	}
}

func TestThemeKeepsAColorThatPasses(t *testing.T) {
	t.Parallel()

	c, _ := brand.ParseColor(brand.DefaultPrimaryColor)
	th := brand.NewTheme(c)

	if th.Light.Primary != c || th.Light.Link != c {
		t.Errorf("light shades of the default changed it: %+v", th.Light)
	}

	if th.Light.Contrast != brand.White {
		t.Errorf("text on the default = %s, want white", th.Light.Contrast)
	}
}

func TestThemeMakesEveryColorReadable(t *testing.T) {
	t.Parallel()

	for _, hex := range []string{"#ffd400", "#0b1f3a", "#7fdbff", "#ffffff", "#000000", "#e4002b", "#00a651"} {
		c, _ := brand.ParseColor(hex)
		th := brand.NewTheme(c)

		check := func(theme string, s brand.Shades, on ...brand.Color) {
			for _, o := range on {
				if got := brand.Contrast(s.Link, o); got < brand.TextContrast {
					t.Errorf("%s %s: link %s on %s has %.2f:1", hex, theme, s.Link, o, got)
				}

				if got := brand.Contrast(s.Primary, o); got < brand.UIContrast {
					t.Errorf("%s %s: button %s on %s has %.2f:1", hex, theme, s.Primary, o, got)
				}
			}

			if got := brand.Contrast(s.Primary, s.Contrast); got < brand.TextContrast {
				t.Errorf("%s %s: text on the button has %.2f:1", hex, theme, got)
			}
		}

		check("light", th.Light, brand.LightBackground, brand.LightSurface)
		check("dark", th.Dark, brand.DarkBackground, brand.DarkSurface)
	}
}

func TestThemeCSS(t *testing.T) {
	t.Parallel()

	c, _ := brand.ParseColor("#ffd400")
	css := brand.NewTheme(c).CSS()

	for _, want := range []string{
		"--wf-primary:#", "--wf-primary-contrast:#000000", "--wf-link:#", "prefers-color-scheme: dark",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("theme CSS has no %q:\n%s", want, css)
		}
	}
}

func TestDetectImage(t *testing.T) {
	t.Parallel()

	const logoPNG = "logo.png"

	png := []byte("\x89PNG\r\n\x1a\n rest")
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8 ")
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)

	good := []struct {
		name string
		data []byte
		want brand.MediaType
	}{
		{"brand/logo.png", png, brand.PNG},
		{"LOGO.PNG", png, brand.PNG},
		{"logo.webp", webp, brand.WebP},
		{"logo.svg", svg, brand.SVG},
	}

	for _, tt := range good {
		got, err := brand.DetectImage(tt.name, tt.data)
		if err != nil || got != tt.want {
			t.Errorf("DetectImage(%s) = %s, %v; want %s", tt.name, got, err, tt.want)
		}
	}

	bad := []struct {
		name string
		data []byte
		want error
	}{
		{"logo.gif", png, brand.ErrImageType},
		{"logo.ico", png, brand.ErrImageType},
		{"logo", png, brand.ErrImageType},
		{logoPNG, svg, brand.ErrImageContent},
		{"logo.svg", png, brand.ErrImageContent},
		{"logo.webp", png, brand.ErrImageContent},
		{logoPNG, nil, brand.ErrImageSize},
		{logoPNG, append(png, make([]byte, brand.MaxImageSize)...), brand.ErrImageSize},
	}

	for _, tt := range bad {
		_, err := brand.DetectImage(tt.name, tt.data)
		if !errors.Is(err, tt.want) {
			t.Errorf("DetectImage(%s, %d bytes) = %v, want %v", tt.name, len(tt.data), err, tt.want)
		}
	}
}
