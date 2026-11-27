package i18n_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

func TestEveryKeyHasEveryLanguage(t *testing.T) {
	t.Parallel()

	for _, l := range i18n.Supported() {
		for k := range i18n.KeyCount() {
			msg := l.T(k)
			if msg == "" {
				t.Errorf("%s: message %d is empty", l, k)
			}
		}
	}
}

func TestVerbsMatchBetweenLanguages(t *testing.T) {
	t.Parallel()

	for k := range i18n.KeyCount() {
		en := strings.Count(i18n.English.T(k), "%s")
		fa := strings.Count(i18n.Persian.T(k), "%s")

		if en != fa {
			t.Errorf("message %d: English has %d verbs, Persian %d", k, en, fa)
		}
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := map[string]i18n.Lang{
		"":                         i18n.English,
		"fa-IR,fa;q=0.9,en;q=0.8":  i18n.Persian,
		"en-US,en;q=0.9,fa;q=0.8":  i18n.English,
		"de-DE":                    i18n.English,
		"de;q=0.9,fa;q=0.5":        i18n.Persian,
		"not a ; valid ;; header=": i18n.English,
	}

	for header, want := range tests {
		if got := i18n.Match(header); got != want {
			t.Errorf("Match(%q) = %s, want %s", header, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	if l, ok := i18n.Parse("fa"); !ok || l != i18n.Persian {
		t.Errorf("Parse(fa) = %s, %v", l, ok)
	}

	if _, ok := i18n.Parse("de"); ok {
		t.Error("Parse(de) accepted an unsupported language")
	}
}

func TestDirection(t *testing.T) {
	t.Parallel()

	if i18n.Persian.Dir() != "rtl" || i18n.English.Dir() != "ltr" {
		t.Errorf("directions: fa %s, en %s", i18n.Persian.Dir(), i18n.English.Dir())
	}
}

func TestPersianDigits(t *testing.T) {
	t.Parallel()

	if got := i18n.Persian.Int(2026); got != "۲۰۲۶" {
		t.Errorf("Int = %q", got)
	}

	if got := i18n.English.Int(int64(42)); got != "42" {
		t.Errorf("Int = %q", got)
	}

	when := time.Date(2026, 11, 27, 9, 5, 0, 0, time.UTC)
	if got := i18n.Persian.Time(when); got != "۲۰۲۶-۱۱-۲۷ ۰۹:۰۵ UTC" {
		t.Errorf("Time = %q", got)
	}

	if got := i18n.Persian.Bytes(1536); got != "۱٫۵ KiB" {
		t.Errorf("Bytes = %q", got)
	}

	if got := i18n.English.Bytes(512); got != "512 B" {
		t.Errorf("Bytes = %q", got)
	}
}

func TestArgumentsKeepTheirDigits(t *testing.T) {
	t.Parallel()

	got := i18n.Persian.T(i18n.Published, "abc123")
	if !strings.Contains(got, "abc123") {
		t.Errorf("T changed a name's digits: %q", got)
	}
}
