package backup

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
)

// Names of the backup CronJob's containers, in the order they run.
const (
	ContainerDump   = "dump"
	ContainerUpload = "upload"
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

	env, site, v := t.env, t.site, t.values

	s, err := domain.SiteOf(v)
	if err != nil {
		return fmt.Errorf("environment %s: %w", env.Env.Name, err)
	}

	cronJob := s.FullName + "-backup"

	job, err := env.Kube.JobFromCronJob(ctx, cronJob, domain.JobName(cronJob, d.Now()))
	if err != nil {
		return fmt.Errorf("%s in %s (is backup.enabled set?): %w", site.Name, env.Env.Name, err)
	}

	errw := cmd.Root().ErrWriter
	_, _ = fmt.Fprintf(errw, "Started Job %s in %s.\n", job.Name, env.Kube.Namespace())

	err = Follow(ctx, env.Kube, job.Name, []string{ContainerDump, ContainerUpload}, cmd.Root().Writer, d.Poll)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Backed up %s in %s.\n", site.Name, env.Env.Name)

	return nil
}
