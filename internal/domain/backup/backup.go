// Package backup is the model of a site's backups as the chart's backup
// CronJob writes them to S3: the stamp each run is named by, which runs a
// retention period keeps, the keys of a run in the bucket, and what a restore
// does. It does no I/O; the s3 package lists the bucket and hands the result
// in.
package backup

import (
	"cmp"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// StampLayout is the CronJob's stamp, date -u +%Y%m%dT%H%M%SZ, in Go's
// notation.
const StampLayout = "20060102T150405Z"

// DumpExt is the extension of a database dump in the bucket.
const DumpExt = ".sql.gz"

// Directories under a site's prefix in the bucket.
const (
	databaseDir = "database"
	currentDir  = "uploads/current"
	changedDir  = "uploads/changed"
)

// day is the CronJob's unit of retention.
const day = 24 * time.Hour

// ErrInvalidStamp is returned for a string that is not a backup stamp.
var ErrInvalidStamp = errors.New("not a backup stamp, want YYYYMMDDTHHMMSSZ")

// ErrNoBackups is returned when a site has no backup to restore.
var ErrNoBackups = errors.New("no backups")

// ErrUnknownStamp is returned when a site has no backup with the stamp.
var ErrUnknownStamp = errors.New("no backup with this stamp")

// Stamp names one backup run: the UTC second the CronJob's upload container
// started. Its string form sorts in time order, which is how the CronJob
// expires old runs.
type Stamp struct {
	t time.Time
}

// NewStamp returns the stamp of t, in UTC and truncated to the second.
func NewStamp(t time.Time) Stamp {
	return Stamp{t: t.UTC().Truncate(time.Second)}
}

// ParseStamp parses a stamp in the CronJob's format.
func ParseStamp(s string) (Stamp, error) {
	t, err := time.Parse(StampLayout, s)
	if err != nil || t.Format(StampLayout) != s {
		return Stamp{}, fmt.Errorf("%q: %w", s, ErrInvalidStamp)
	}

	return Stamp{t: t}, nil
}

// String formats the stamp as the CronJob does.
func (s Stamp) String() string {
	return s.t.Format(StampLayout)
}

// Time is the stamp as a time in UTC.
func (s Stamp) Time() time.Time {
	return s.t
}

// IsZero reports whether the stamp is the zero stamp.
func (s Stamp) IsZero() bool {
	return s.t.IsZero()
}

// Compare orders stamps in time: -1 when s is earlier than o, 0 when equal,
// +1 when later.
func (s Stamp) Compare(o Stamp) int {
	return s.t.Compare(o.t)
}

// MarshalText writes the stamp in the CronJob's format, so JSON shows it as
// the bucket does.
func (s Stamp) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// UnmarshalText parses a stamp in the CronJob's format.
func (s *Stamp) UnmarshalText(text []byte) error {
	parsed, err := ParseStamp(string(text))
	if err != nil {
		return err
	}

	*s = parsed

	return nil
}

// Prefix is the directory of a site's backups in the bucket:
// <namespace>/<site>, the CronJob's PREFIX.
func Prefix(namespace, site string) string {
	return namespace + "/" + site
}

// DatabaseDir is the directory of a site's dumps under its prefix, with a
// trailing slash, ready to list.
func DatabaseDir(prefix string) string {
	return path.Join(prefix, databaseDir) + "/"
}

// ChangedDir is the directory of the upload files each run replaced or
// deleted, with a trailing slash, ready to list.
func ChangedDir(prefix string) string {
	return path.Join(prefix, changedDir) + "/"
}

// DumpKey is the key of a run's database dump.
func DumpKey(prefix string, stamp Stamp) string {
	return path.Join(prefix, databaseDir, stamp.String()+DumpExt)
}

// CurrentKey is the directory of the mirror of the uploads, as of the latest
// run.
func CurrentKey(prefix string) string {
	return path.Join(prefix, currentDir)
}

// ChangedKey is the directory of what a run replaced or deleted in the
// uploads.
func ChangedKey(prefix string, stamp Stamp) string {
	return path.Join(prefix, changedDir, stamp.String())
}

// StampOfDump returns the stamp of a dump's file name, such as
// 20261126T031500Z.sql.gz; ok is false for any other name.
func StampOfDump(name string) (Stamp, bool) {
	stem, ok := strings.CutSuffix(name, DumpExt)
	s, err := ParseStamp(stem)

	return s, ok && err == nil
}

// Backup is one run of a site's backup as the bucket holds it.
type Backup struct {
	// Stamp names the run.
	Stamp Stamp `json:"stamp"`
	// DumpSize is the size of the database dump in bytes; 0 when the run
	// left no dump.
	DumpSize int64 `json:"dumpSize"`
	// HasDump says whether the run's dump is in the bucket.
	HasDump bool `json:"hasDump"`
	// ChangedFiles counts the upload files the run replaced or deleted.
	ChangedFiles int `json:"changedFiles"`
}

// Merge joins the dumps and the counts of changed upload files of a site into
// one list of runs, newest first. A run appears when it has either.
func Merge(dumps map[Stamp]int64, changed map[Stamp]int) []Backup {
	byStamp := make(map[Stamp]*Backup, len(dumps))

	get := func(s Stamp) *Backup {
		b, ok := byStamp[s]
		if !ok {
			b = &Backup{Stamp: s, DumpSize: 0, HasDump: false, ChangedFiles: 0}
			byStamp[s] = b
		}

		return b
	}

	for s, size := range dumps {
		b := get(s)
		b.DumpSize, b.HasDump = size, true
	}

	for s, n := range changed {
		get(s).ChangedFiles = n
	}

	out := make([]Backup, 0, len(byStamp))
	for _, b := range byStamp {
		out = append(out, *b)
	}

	slices.SortFunc(out, func(a, b Backup) int { return b.Stamp.Compare(a.Stamp) })

	return out
}

// Latest returns the newest run that has a dump.
func Latest(backups []Backup) (Backup, error) {
	var (
		latest Backup
		found  bool
	)

	for _, b := range backups {
		if b.HasDump && (!found || b.Stamp.Compare(latest.Stamp) > 0) {
			latest, found = b, true
		}
	}

	if !found {
		return Backup{}, ErrNoBackups
	}

	return latest, nil
}

// Find returns the run with the stamp, which must have a dump.
func Find(backups []Backup, stamp Stamp) (Backup, error) {
	i := slices.IndexFunc(backups, func(b Backup) bool { return b.HasDump && b.Stamp == stamp })
	if i < 0 {
		return Backup{}, fmt.Errorf("%s: %w", stamp, ErrUnknownStamp)
	}

	return backups[i], nil
}

// Cutoff is the stamp before which the CronJob deletes runs when it runs at
// now with keepDays: now minus keepDays days, compared as stamps.
func Cutoff(now time.Time, keepDays int) Stamp {
	return NewStamp(now.Add(-time.Duration(keepDays) * day))
}

// Expires is when the CronJob's first run after that moment deletes a run
// kept for keepDays.
func Expires(stamp Stamp, keepDays int) time.Time {
	return stamp.Time().Add(time.Duration(keepDays) * day)
}

// Expired returns the runs the CronJob deletes when it runs at now with
// keepDays: those whose stamp sorts before the cutoff.
func Expired(backups []Backup, now time.Time, keepDays int) []Backup {
	cutoff := Cutoff(now, keepDays)

	return slices.DeleteFunc(slices.Clone(backups), func(b Backup) bool {
		return b.Stamp.Compare(cutoff) >= 0
	})
}

// RestorePlan is what a restore does: which dump it imports, and with
// uploads, how it rebuilds the uploads of the run.
type RestorePlan struct {
	// Prefix is the site's directory in the bucket the backup is read from.
	Prefix string `json:"prefix"`
	// Stamp names the run restored.
	Stamp Stamp `json:"stamp"`
	// Dump is the key of the dump imported.
	Dump string `json:"dump"`
	// Uploads says whether the uploads are restored too.
	Uploads bool `json:"uploads"`
	// Overlays are the runs after Stamp, newest first, whose replaced and
	// deleted files are copied over the mirror to wind the uploads back to
	// Stamp. Empty when Stamp is the latest run.
	Overlays []Stamp `json:"overlays,omitempty"`
}

// PlanRestore plans restoring the run with stamp from the runs of the site
// under prefix. With uploads, the restore syncs the mirror, which is as of the
// latest run, and then copies over it what each later run replaced or
// deleted, newest first, which puts every file those runs touched back as it
// was at stamp. Files first uploaded after stamp stay, because the bucket does
// not record additions.
func PlanRestore(prefix string, backups []Backup, stamp Stamp, uploads bool) (RestorePlan, error) {
	_, err := Find(backups, stamp)
	if err != nil {
		return RestorePlan{}, err
	}

	plan := RestorePlan{Prefix: prefix, Stamp: stamp, Dump: DumpKey(prefix, stamp), Uploads: uploads, Overlays: nil}

	if uploads {
		for _, b := range backups {
			if b.ChangedFiles > 0 && b.Stamp.Compare(stamp) > 0 {
				plan.Overlays = append(plan.Overlays, b.Stamp)
			}
		}

		slices.SortFunc(plan.Overlays, func(a, b Stamp) int { return b.Compare(a) })
	}

	return plan, nil
}

// Exact reports whether the plan restores the uploads exactly as they were at
// its stamp; false when later runs may have added files that stay.
func (p RestorePlan) Exact() bool {
	return !p.Uploads || len(p.Overlays) == 0
}

// JobName names a Job after base and the stamp of now, cut so that the name
// stays a DNS label of at most 63 characters, e.g. blog-restore-20261126t101500z.
func JobName(base string, now time.Time) string {
	const maxName = 63

	suffix := "-" + strings.ToLower(NewStamp(now).String())
	base = strings.TrimRight(base[:min(len(base), maxName-len(suffix))], "-")

	return cmp.Or(base, "wpfleet") + suffix
}
