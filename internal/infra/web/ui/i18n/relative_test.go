package i18n_test

import (
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

func TestAgo(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		lang i18n.Lang
		ago  time.Duration
		want string
	}{
		{i18n.English, 10 * time.Second, "just now"},
		{i18n.English, -time.Minute, "just now"},
		{i18n.English, time.Minute, "1 minute ago"},
		{i18n.English, 5 * time.Minute, "5 minutes ago"},
		{i18n.English, 3 * time.Hour, "3 hours ago"},
		{i18n.English, 26 * time.Hour, "1 day ago"},
		{i18n.English, 30 * 24 * time.Hour, "28 Oct 2026"},
		{i18n.Persian, 3 * time.Hour, "۳ ساعت پیش"},
		{i18n.Persian, 30 * 24 * time.Hour, "۶ آبان ۱۴۰۵"},
	}

	for _, tt := range tests {
		if got := tt.lang.Ago(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("%s Ago(%s) = %q, want %q", tt.lang, tt.ago, got, tt.want)
		}
	}
}

func TestDay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		lang i18n.Lang
		when time.Time
		want string
	}{
		{i18n.English, time.Date(2026, 11, 27, 3, 12, 0, 0, time.UTC), "Today at 03:12 UTC"},
		{i18n.English, time.Date(2026, 11, 26, 3, 12, 0, 0, time.UTC), "Yesterday at 03:12 UTC"},
		{i18n.English, time.Date(2026, 11, 20, 23, 5, 0, 0, time.UTC), "20 Nov 2026 at 23:05 UTC"},
		{i18n.Persian, time.Date(2026, 11, 26, 3, 12, 0, 0, time.UTC), "دیروز ساعت ۰۳:۱۲ (UTC)"},
		{i18n.Persian, time.Date(2026, 11, 20, 23, 5, 0, 0, time.UTC), "۲۹ آبان ۱۴۰۵ ساعت ۲۳:۰۵ (UTC)"},
	}

	for _, tt := range tests {
		if got := tt.lang.Day(tt.when, now); got != tt.want {
			t.Errorf("%s Day(%s) = %q, want %q", tt.lang, tt.when, got, tt.want)
		}
	}
}

// The Persian dates are in the Solar Hijri calendar; Nowruz and the leap
// years around it are where a conversion goes wrong.
func TestPersianDate(t *testing.T) {
	t.Parallel()

	tests := map[time.Time]string{
		time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC):  "۱ فروردین ۱۴۰۳",
		time.Date(2024, 3, 19, 0, 0, 0, 0, time.UTC):  "۲۹ اسفند ۱۴۰۲",
		time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC):  "۳۰ اسفند ۱۴۰۳",
		time.Date(2025, 3, 21, 0, 0, 0, 0, time.UTC):  "۱ فروردین ۱۴۰۴",
		time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC): "۶ آذر ۱۴۰۵",
		time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC):  "۳۱ شهریور ۱۴۰۵",
		time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC):  "۱ مهر ۱۴۰۵",
	}

	for when, want := range tests {
		if got := i18n.Persian.Date(when); got != want {
			t.Errorf("Date(%s) = %q, want %q", when.Format(time.DateOnly), got, want)
		}
	}
}

func TestCount(t *testing.T) {
	t.Parallel()

	if got := i18n.English.Count(1, i18n.SiteCountOne, i18n.SiteCountMany); got != "1 site" {
		t.Errorf("Count(1) = %q", got)
	}

	if got := i18n.English.Count(7, i18n.SiteCountOne, i18n.SiteCountMany); got != "7 sites" {
		t.Errorf("Count(7) = %q", got)
	}

	if got := i18n.Persian.Count(12, i18n.SiteCountOne, i18n.SiteCountMany); got != "۱۲ سایت" {
		t.Errorf("Count(12) = %q", got)
	}
}
