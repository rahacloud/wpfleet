// Package i18n is the message catalogue of the web UI: every text a person
// sees, in English and in Persian, under typed keys. A language also knows
// its writing direction and writes numbers and dates in its own digits.
package i18n

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/language"
)

// Lang is a language of the UI, as its BCP 47 code.
type Lang string

// The languages of the UI.
const (
	// English is the default language.
	English Lang = "en"
	// Persian is written right to left, with Persian digits.
	Persian Lang = "fa"
)

// Supported lists the languages of the UI, the default first.
func Supported() []Lang {
	return []Lang{English, Persian}
}

// Parse returns the language with the given code, and false for a code that
// is not supported.
func Parse(code string) (Lang, bool) {
	switch Lang(code) {
	case English, Persian:
		return Lang(code), true
	default:
		return English, false
	}
}

// Match picks the language for an Accept-Language header: the supported
// language the browser prefers, else English.
func Match(acceptLanguage string) Lang {
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return English
	}

	matcher := language.NewMatcher([]language.Tag{language.English, language.Persian})

	_, index, confidence := matcher.Match(tags...)
	if confidence == language.No {
		return English
	}

	return Supported()[index]
}

// Code is the language's BCP 47 code, for the lang attribute.
func (l Lang) Code() string {
	return string(l)
}

// Dir is the writing direction, for the dir attribute: rtl or ltr.
func (l Lang) Dir() string {
	if l == Persian {
		return "rtl"
	}

	return "ltr"
}

// Name is the language's name in itself, for the language switch.
func (l Lang) Name() string {
	if l == Persian {
		return "فارسی"
	}

	return "English"
}

// T returns the message of key in the language with args put in its %s
// verbs. Numbers and dates go through Int, Bytes and Time first, so they are
// written in the language's digits; names (sites, environments, commits) are
// passed as they are.
func (l Lang) T(key Key, args ...string) string {
	msg := l.catalogue()[key]
	if len(args) == 0 {
		return msg
	}

	values := make([]any, len(args))
	for i, a := range args {
		values[i] = a
	}

	return fmt.Sprintf(msg, values...)
}

// Int writes an integer in the language's digits.
func (l Lang) Int[T ~int | ~int64](n T) string {
	return l.digits(strconv.FormatInt(int64(n), 10))
}

// kibi is the step between two byte units.
const kibi = 1024

// Bytes writes a size in bytes with a binary unit, in the language's digits.
func (l Lang) Bytes(n int64) string {
	byteUnits := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	size := float64(n)
	unit := 0

	for size >= kibi && unit < len(byteUnits)-1 {
		size /= kibi
		unit++
	}

	text := strconv.FormatFloat(size, 'f', 1, 64)
	if unit == 0 {
		text = strconv.FormatInt(n, 10)
	}

	return l.digits(text) + " " + byteUnits[unit]
}

// timeLayout is how the UI writes a time: ISO dates read the same in both
// languages, and UTC keeps a team in several time zones on one clock.
const timeLayout = "2006-01-02 15:04 UTC"

// Time writes a time in UTC in the language's digits.
func (l Lang) Time(t time.Time) string {
	return l.digits(t.UTC().Format(timeLayout))
}

// catalogue is the language's messages.
func (l Lang) catalogue() *[keyCount]string {
	if l == Persian {
		return &persian
	}

	return &english
}

// digits writes the ASCII digits and the decimal point of s in the
// language's digits.
func (l Lang) digits(s string) string {
	if l != Persian {
		return s
	}

	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return '۰' + (r - '0')
		case r == '.':
			return '٫'
		default:
			return r
		}
	}, s)
}
