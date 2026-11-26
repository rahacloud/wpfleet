package backup

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
)

// timeLayout is how list prints times.
const timeLayout = "2006-01-02 15:04 MST"

// Row is one line of backup list.
type Row struct {
	domain.Backup

	// Expires is when the CronJob deletes the run, by backup.keepDays.
	Expires time.Time `json:"expires"`
}

func listCommand(d Deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "list",
		Usage:     "list the site's backups in S3: stamp, dump size, changed upload files, expiry",
		Arguments: siteArgument(),
		Action:    d.list,
	}
}

func (d Deps) list(ctx context.Context, cmd *cli.Command) error {
	t, err := d.target(cmd)
	if err != nil {
		return err
	}

	env, site, v := t.env, t.site, t.values

	keep, err := strconv.Atoi(v.String(domain.ValueKeepDays))
	if err != nil {
		return fmt.Errorf("%s in %s: %s: %w", site.Name, env.Env.Name, domain.ValueKeepDays, err)
	}

	bucket, err := d.Bucket(env.Fleet)
	if err != nil {
		return err
	}

	runs, err := bucket.Backups(ctx, domain.Prefix(env.Env.Namespace, site.Name))
	if err != nil {
		return err
	}

	rows := make([]Row, 0, len(runs))
	for _, b := range runs {
		rows = append(rows, Row{Backup: b, Expires: domain.Expires(b.Stamp, keep)})
	}

	return cmdutil.Print(cmd, rows,
		cmdutil.Column[Row]{Header: "stamp", Value: func(r Row) string { return r.Stamp.String() }},
		cmdutil.Column[Row]{Header: "dump", Value: func(r Row) string {
			if !r.HasDump {
				return ""
			}

			return Size(r.DumpSize)
		}},
		cmdutil.Column[Row]{Header: "changed", Value: func(r Row) string { return strconv.Itoa(r.ChangedFiles) }},
		cmdutil.Column[Row]{Header: "expires", Value: func(r Row) string { return r.Expires.Format(timeLayout) }},
	)
}

// Size formats a byte count with a binary unit, e.g. 1.5 MiB.
func Size(n int64) string {
	const unit = 1024

	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}

	value, exp := float64(n)/unit, 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}

	return strconv.FormatFloat(value, 'f', 1, 64) + " " + string("KMGTP"[exp]) + "iB"
}
