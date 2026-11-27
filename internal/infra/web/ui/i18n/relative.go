package i18n

import "time"

// The steps between the units of a relative time.
const (
	hoursPerDay = 24
	// daysInWords is how many days back Ago still counts in days; older
	// times are written as a date.
	daysInWords = 14
)

// Ago says in words how long before now t was: "just now", "5 minutes ago",
// "3 hours ago", "2 days ago", and the date for anything older than two
// weeks. A time after now is "just now", since a clock a little ahead on
// another machine should not read as the future.
func (l Lang) Ago(t, now time.Time) string {
	d := now.Sub(t)

	switch {
	case d < time.Minute:
		return l.T(JustNow)
	case d < time.Hour:
		return l.counted(int(d/time.Minute), MinuteAgo, MinutesAgo)
	case d < hoursPerDay*time.Hour:
		return l.counted(int(d/time.Hour), HourAgo, HoursAgo)
	case d < daysInWords*hoursPerDay*time.Hour:
		return l.counted(int(d/(hoursPerDay*time.Hour)), DayAgo, DaysAgo)
	default:
		return l.Date(t)
	}
}

// Day says when t was the way people say it, in UTC: "Today at 03:12 UTC",
// "Yesterday at 03:12 UTC", else the date and the time.
func (l Lang) Day(t, now time.Time) string {
	t, now = t.UTC(), now.UTC()
	clock := l.digits(t.Format("15:04"))
	day := civil(t)

	switch day {
	case civil(now):
		return l.T(TodayAt, clock)
	case civil(now.AddDate(0, 0, -1)):
		return l.T(YesterdayAt, clock)
	default:
		return l.T(DateAt, l.Date(t), clock)
	}
}

// Date writes the day of t in UTC: "26 Nov 2026" in English, and in the
// Solar Hijri calendar Iran uses in Persian, "۶ آذر ۱۴۰۵".
func (l Lang) Date(t time.Time) string {
	t = t.UTC()

	if l != Persian {
		return t.Format("2 Jan 2006")
	}

	y, m, d := jalali(t.Year(), int(t.Month()), t.Day())

	return l.Int(d) + " " + persianMonths()[m-1] + " " + l.Int(y)
}

// Count writes a number of things with the right form of the noun: one is
// the message for exactly one, many for every other number.
func (l Lang) Count(n int, one, many Key) string {
	return l.counted(n, one, many)
}

// counted is n in the language's digits put into one or many.
func (l Lang) counted(n int, one, many Key) string {
	if n == 1 {
		return l.T(one, l.Int(n))
	}

	return l.T(many, l.Int(n))
}

// civilDay is a calendar day, to compare two times by their day.
type civilDay struct {
	year  int
	month time.Month
	day   int
}

// civil is the calendar day of t in its location.
func civil(t time.Time) civilDay {
	y, m, d := t.Date()

	return civilDay{year: y, month: m, day: d}
}

// persianMonths are the months of the Solar Hijri calendar.
func persianMonths() [12]string {
	return [12]string{
		"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور",
		"مهر", "آبان", "آذر", "دی", "بهمن", "اسفند",
	}
}

// The constants of the Gregorian to Solar Hijri conversion, from the
// arithmetic of jalaali-js (MIT).
const (
	jalaliEpochDays = 355666
	jalaliCycle     = 12053 // days in 33 years
	jalaliCycleYrs  = 33
	jalaliQuad      = 1461 // days in 4 years
	jalaliBaseYear  = -1595
	daysInYear      = 365
	firstHalfDays   = 186 // the first six months have 31 days each
	longMonth       = 31
	shortMonth      = 30
	firstHalfMonths = 6
	leapEvery       = 4
	centuryYears    = 100
	quadCentury     = 400
)

// jalali converts a Gregorian date to the Solar Hijri calendar.
func jalali(gy, gm, gd int) (int, int, int) {
	daysBefore := [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}

	gy2 := gy
	if gm > int(time.February) {
		gy2 = gy + 1
	}

	days := jalaliEpochDays + daysInYear*gy + (gy2+leapEvery-1)/leapEvery - (gy2+centuryYears-1)/centuryYears +
		(gy2+quadCentury-1)/quadCentury + gd + daysBefore[gm-1]

	jy := jalaliBaseYear + jalaliCycleYrs*(days/jalaliCycle)
	days %= jalaliCycle
	jy += leapEvery * (days / jalaliQuad)
	days %= jalaliQuad

	if days > daysInYear {
		jy += (days - 1) / daysInYear
		days = (days - 1) % daysInYear
	}

	if days < firstHalfDays {
		return jy, 1 + days/longMonth, 1 + days%longMonth
	}

	return jy, firstHalfMonths + 1 + (days-firstHalfDays)/shortMonth, 1 + (days-firstHalfDays)%shortMonth
}
