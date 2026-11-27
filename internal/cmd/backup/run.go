package backup

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

func runCommand(d Deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "run",
		Usage:     "start a Job from the site's backup CronJob now, follow its logs, and fail when it fails",
		Arguments: siteArgument(),
		Action:    d.run,
	}
}

func (d Deps) run(ctx context.Context, cmd *cli.Command) error {
	t, err := d.target(cmd)
	if err != nil {
		return err
	}

	_, err = ops.RunBackup(ctx, t.env, t.site, d.Options(), cmdutil.ProgressTo(cmd))

	return err
}
