package backup

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Flags of restore.
const (
	flagStamp   = "stamp"
	flagUploads = "uploads"
	flagYes     = "yes"
)

func restoreCommand(d Deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "restore",
		Usage: "import a backup into the site's database, and with --uploads its uploads, through a Job",
		Description: "The Job runs in the site's namespace with the chart's backup images: it copies the dump from S3, " +
			"with --uploads syncs the uploads back into the site's volume, and imports the dump with the site's own " +
			"database user. Uploads of an older backup are the latest mirror wound back with what later runs replaced " +
			"or deleted; files first uploaded after that backup stay.",
		Arguments: siteArgument(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     flagStamp,
				Usage:    "the backup to restore, as backup list prints it, or latest",
				Required: true,
			},
			&cli.BoolFlag{Name: flagUploads, Usage: "restore the uploads too"},
			&cli.BoolFlag{Name: flagYes, Aliases: []string{"y"}, Usage: "do not ask for confirmation"},
		},
		Action: d.restore,
	}
}

func (d Deps) restore(ctx context.Context, cmd *cli.Command) error {
	t, err := d.target(cmd)
	if err != nil {
		return err
	}

	opts := ops.RestoreOptions{BackupOptions: d.Options(), Stamp: cmd.String(flagStamp), Uploads: cmd.Bool(flagUploads)}

	spec, err := ops.PlanRestore(ctx, t.env, t.site, opts)
	if err != nil {
		return err
	}

	Describe(cmd, spec)

	err = Confirm(cmd, cmd.Bool(flagYes), fmt.Sprintf("Overwrite %s in %s?", t.site.Name, t.env.Env.Name))
	if err != nil {
		return err
	}

	return ops.Restore(ctx, t.env, spec, d.Options(), cmdutil.ProgressTo(cmd))
}

// Describe tells the user on stderr what a restore is about to do.
func Describe(cmd *cli.Command, spec ops.RestoreSpec) {
	for _, line := range ops.DescribeRestore(spec) {
		_, _ = fmt.Fprintln(cmd.Root().ErrWriter, line)
	}
}
