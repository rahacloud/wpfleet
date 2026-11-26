package backup_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func stamp(t *testing.T, s string) backup.Stamp {
	t.Helper()

	st, err := backup.ParseStamp(s)
	testutil.NoError(t, err)

	return st
}

func TestParseStamp(t *testing.T) {
	t.Parallel()

	s := stamp(t, "20261126T031500Z")
	testutil.Equal(t, "string", s.String(), "20261126T031500Z")
	testutil.True(t, "time", s.Time().Equal(time.Date(2026, 11, 26, 3, 15, 0, 0, time.UTC)))

	for _, bad := range []string{
		"", "20261126", "20261126T031500", "2026-11-26T03:15:00Z", "20261332T031500Z", "20261126t031500z",
	} {
		_, err := backup.ParseStamp(bad)
		testutil.ErrorIs(t, err, backup.ErrInvalidStamp)
	}
}

func TestNewStampIsUTC(t *testing.T) {
	t.Parallel()

	halfHour := time.FixedZone("plus-three-thirty", 3*3600+1800)
	s := backup.NewStamp(time.Date(2026, 11, 26, 6, 45, 1, 999, halfHour))
	testutil.Equal(t, "stamp", s.String(), "20261126T031501Z")
}

func TestStampJSON(t *testing.T) {
	t.Parallel()

	b := backup.Backup{Stamp: stamp(t, "20261126T031500Z"), DumpSize: 3, HasDump: true, ChangedFiles: 0}

	data, err := json.Marshal(b)
	testutil.NoError(t, err)
	testutil.Contains(t, "json", string(data), `"stamp":"20261126T031500Z"`)

	var back backup.Backup
	testutil.NoError(t, json.Unmarshal(data, &back))
	testutil.Equal(t, "round trip", back.Stamp, stamp(t, "20261126T031500Z"))

	testutil.ErrorIs(t, json.Unmarshal([]byte(`{"stamp":"yesterday"}`), &back), backup.ErrInvalidStamp)
}

func TestKeys(t *testing.T) {
	t.Parallel()

	p := backup.Prefix("wp-staging", "blog")
	s := stamp(t, "20261126T031500Z")

	testutil.Equal(t, "dump", backup.DumpKey(p, s), "wp-staging/blog/database/20261126T031500Z.sql.gz")
	testutil.Equal(t, "database dir", backup.DatabaseDir(p), "wp-staging/blog/database/")
	testutil.Equal(t, "changed dir", backup.ChangedDir(p), "wp-staging/blog/uploads/changed/")
	testutil.Equal(t, "changed", backup.ChangedKey(p, s), "wp-staging/blog/uploads/changed/20261126T031500Z")
	testutil.Equal(t, "current", backup.CurrentKey(p), "wp-staging/blog/uploads/current")

	got, ok := backup.StampOfDump("20261126T031500Z.sql.gz")
	testutil.True(t, "dump name", ok && got == s)

	_, ok = backup.StampOfDump("20261126T031500Z.sql")
	testutil.True(t, "not a dump", !ok)

	_, ok = backup.StampOfDump("latest.sql.gz")
	testutil.True(t, "not a stamp", !ok)
}

// runs is three nights: the middle one has no dump, the last changed nothing.
func runs(t *testing.T) []backup.Backup {
	t.Helper()

	return backup.Merge(
		map[backup.Stamp]int64{
			stamp(t, "20261124T031500Z"): 100,
			stamp(t, "20261126T031500Z"): 300,
		},
		map[backup.Stamp]int{
			stamp(t, "20261124T031500Z"): 2,
			stamp(t, "20261125T031500Z"): 5,
		},
	)
}

func TestMergeAndLatest(t *testing.T) {
	t.Parallel()

	got := runs(t)
	testutil.Equal(t, "runs", len(got), 3)
	testutil.Equal(t, "newest first", got[0].Stamp.String(), "20261126T031500Z")
	middle := backup.Backup{Stamp: stamp(t, "20261125T031500Z"), DumpSize: 0, HasDump: false, ChangedFiles: 5}
	testutil.DeepEqual(t, "middle", got[1], middle)

	latest, err := backup.Latest(got)
	testutil.NoError(t, err)
	testutil.Equal(t, "latest", latest.DumpSize, int64(300))

	_, err = backup.Latest(got[1:2])
	testutil.ErrorIs(t, err, backup.ErrNoBackups)

	_, err = backup.Find(got, stamp(t, "20261125T031500Z"))
	testutil.ErrorIs(t, err, backup.ErrUnknownStamp)
}

func TestRetention(t *testing.T) {
	t.Parallel()

	expires := backup.Expires(stamp(t, "20261124T031500Z"), 14)
	testutil.True(t, "expires", expires.Equal(time.Date(2026, 12, 8, 3, 15, 0, 0, time.UTC)))
}

func TestPlanRestore(t *testing.T) {
	t.Parallel()

	all := runs(t)
	prefix := backup.Prefix("wp-staging", "blog")

	plan, err := backup.PlanRestore(prefix, all, stamp(t, "20261126T031500Z"), true)
	testutil.NoError(t, err)
	testutil.Equal(t, "dump", plan.Dump, "wp-staging/blog/database/20261126T031500Z.sql.gz")
	testutil.Equal(t, "latest needs no overlay", len(plan.Overlays), 0)
	testutil.True(t, "exact", plan.Exact())

	plan, err = backup.PlanRestore(prefix, all, stamp(t, "20261124T031500Z"), true)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "overlays", plan.Overlays, []backup.Stamp{stamp(t, "20261125T031500Z")})
	testutil.True(t, "not exact", !plan.Exact())

	plan, err = backup.PlanRestore(prefix, all, stamp(t, "20261124T031500Z"), false)
	testutil.NoError(t, err)
	testutil.True(t, "database only", plan.Overlays == nil && plan.Exact())

	_, err = backup.PlanRestore(prefix, all, stamp(t, "20261125T031500Z"), true)
	testutil.ErrorIs(t, err, backup.ErrUnknownStamp)
}

func TestJobName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 26, 10, 15, 0, 0, time.UTC)
	testutil.Equal(t, "short", backup.JobName("blog-restore", now), "blog-restore-20261126t101500z")

	long := backup.JobName(strings.Repeat("a", 49)+"-backup", now)
	testutil.Equal(t, "length", len(long), 63)
	testutil.True(t, "no double hyphen", !strings.Contains(long, "--"))

	cut := backup.JobName(strings.Repeat("a", 45)+"-b-restore", now)
	testutil.Equal(t, "cut at a hyphen", cut, strings.Repeat("a", 45)+"-20261126t101500z")
}
