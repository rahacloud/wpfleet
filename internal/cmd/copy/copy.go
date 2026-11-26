// Package copy is wpfleet copy: restore one environment's latest backup of a
// site into another environment, then move the site to the target's host
// with wp-cli search-replace and the page builder fix-up.
package copy

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/backup"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/assets"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
)

// Flags of copy.
const (
	flagFrom          = "from"
	flagTo            = "to"
	flagSkipUploads   = "skip-uploads"
	flagYes           = "yes"
	flagYesProduction = "yes-production"
)

// production is the environment copy refuses to write into without
// --yes-production.
const production = "production"

// rehostRemote is where the page builder fix-up is copied in the pod.
const rehostRemote = "/tmp/wpfleet-" + assets.RehostExtraName

// ErrSameEnvironment is returned when --from and --to are the same.
var ErrSameEnvironment = errors.New("--from and --to must be different environments")

// ErrProduction is returned for --to production without --yes-production.
var ErrProduction = errors.New("copying into production overwrites its database; pass --yes-production")

// Command is wpfleet copy.
func Command() *cli.Command {
	return NewCommand(backup.DefaultDeps())
}

// NewCommand is wpfleet copy with the given dependencies.
func NewCommand(d backup.Deps) *cli.Command {
	c := copier{d: d}

	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "copy",
		Usage: "restore a site's latest backup from one environment into another and move it to the target's host",
		Description: "The target must be able to read the source's backups: both write to the same bucket, and the " +
			"target namespace's S3 secret can read the source's prefix. After the restore, wp-cli search-replace moves " +
			"the old host to the new one (--network on a multisite), the page builder fix-up rewrites what " +
			"search-replace cannot see, and the object cache is flushed.",
		Arguments: backup.SiteArgument(),
		Flags: []cli.Flag{
			&cli.StringFlag{Name: flagFrom, Usage: "the environment to copy from", Required: true},
			&cli.StringFlag{Name: flagTo, Usage: "the environment to copy into", Required: true},
			&cli.BoolFlag{Name: flagSkipUploads, Usage: "copy the database only, not the uploads"},
			&cli.BoolFlag{Name: flagYes, Aliases: []string{"y"}, Usage: "do not ask for confirmation"},
			&cli.BoolFlag{Name: flagYesProduction, Usage: "allow --to production"},
		},
		Action: c.copy,
	}
}

// copier runs wpfleet copy.
type copier struct {
	d backup.Deps
}

// side is one end of a copy: an environment, the site in it and its values.
type side struct {
	env  *cmdutil.Env
	site fleet.Site
	host string
}

func (c copier) copy(ctx context.Context, cmd *cli.Command) error {
	name, err := backup.SiteArg(cmd)
	if err != nil {
		return err
	}

	from, to, err := environments(cmd)
	if err != nil {
		return err
	}

	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	src, err := c.open(f, from, name)
	if err != nil {
		return err
	}

	dst, err := c.open(f, to, name)
	if err != nil {
		return err
	}

	spec, multisite, err := c.plan(ctx, f, src, dst, !cmd.Bool(flagSkipUploads))
	if err != nil {
		return err
	}

	wp, cluster, err := c.ready(ctx, f, dst)
	if err != nil {
		return err
	}

	errw, out := cmd.Root().ErrWriter, cmd.Root().Writer

	backup.Describe(errw, to, spec)
	_, _ = fmt.Fprintf(errw, "Then moving %s to %s.\n", src.host, dst.host)

	err = backup.Confirm(cmd, cmd.Bool(flagYes), fmt.Sprintf("Overwrite %s in %s with %s's?", name, to, from))
	if err != nil {
		return err
	}

	err = c.d.Restore(ctx, cluster, spec, out, errw)
	if err != nil {
		return err
	}

	return rehost(ctx, wp, dst, src.host, multisite, out, errw)
}

// ready checks, before anything is overwritten, that the target has a running
// pod to move the host in and that wp-cli is at hand, and returns the runner
// and the target's cluster.
//
//nolint:ireturn // the cluster is a fake in tests
func (c copier) ready(ctx context.Context, f *cmdutil.Fleet, dst side) (*wpcli.Runner, backup.Cluster, error) {
	cluster, err := backup.ClusterOf(dst.env)
	if err != nil {
		return nil, nil, err
	}

	_, err = cluster.RunningPod(ctx, dst.site.Name)
	if err != nil {
		return nil, nil, err
	}

	wp, err := c.d.WPCLI(ctx, f, cluster)
	if err != nil {
		return nil, nil, err
	}

	return wp, cluster, nil
}

// environments returns --from and --to, refusing the same environment twice
// and production without --yes-production.
func environments(cmd *cli.Command) (string, string, error) {
	from, to := cmd.String(flagFrom), cmd.String(flagTo)

	switch {
	case from == to:
		return "", "", ErrSameEnvironment
	case to == production && !cmd.Bool(flagYesProduction):
		return "", "", ErrProduction
	}

	return from, to, nil
}

// open opens an environment and finds the site and its host there.
func (c copier) open(f *cmdutil.Fleet, envName, name string) (side, error) {
	env, err := c.d.Open(f, envName)
	if err != nil {
		return side{}, err
	}

	site, err := env.Site(name)
	if err != nil {
		return side{}, err
	}

	v, err := env.Values(site)
	if err != nil {
		return side{}, err
	}

	host, err := fleet.Host(v)
	if err != nil {
		return side{}, fmt.Errorf("%s in %s: %w", name, envName, err)
	}

	return side{env: env, site: site, host: host}, nil
}

// plan finds the source's latest backup and builds its restore into the
// target, with the uploads when uploads is set. It also says whether the
// target is a multisite.
func (c copier) plan(
	ctx context.Context, f *cmdutil.Fleet, src, dst side, uploads bool,
) (backup.RestoreSpec, bool, error) {
	bucket, err := c.d.Bucket(f)
	if err != nil {
		return backup.RestoreSpec{}, false, err
	}

	prefix := domain.Prefix(src.env.Env.Namespace, src.site.Name)

	runs, err := bucket.Backups(ctx, prefix)
	if err != nil {
		return backup.RestoreSpec{}, false, err
	}

	latest, err := domain.Latest(runs)
	if err != nil {
		return backup.RestoreSpec{}, false, fmt.Errorf("%s in %s: %w", src.site.Name, src.env.Env.Name, err)
	}

	plan, err := domain.PlanRestore(prefix, runs, latest.Stamp, uploads)
	if err != nil {
		return backup.RestoreSpec{}, false, err
	}

	v, err := backup.ChartValues(dst.env, dst.site)
	if err != nil {
		return backup.RestoreSpec{}, false, err
	}

	spec, err := c.d.Spec(dst.env, v, plan, bucket.Bucket())
	if err != nil {
		return backup.RestoreSpec{}, false, err
	}

	return spec, domain.Multisite(v), nil
}

// rehost moves the restored site from oldHost to its own host and flushes
// its cache, in a running pod of the target, looked up again because the
// restore may have taken a while.
func rehost(
	ctx context.Context, wp *wpcli.Runner, dst side, oldHost string, multisite bool, out, errw io.Writer,
) error {
	pod, err := dst.env.Kube.RunningPod(ctx, dst.site.Name)
	if err != nil {
		return err
	}

	if oldHost == dst.host {
		_, _ = fmt.Fprintf(errw, "Both environments serve %s; no host to move.\n", oldHost)
	} else {
		err = moveHost(ctx, wp, dst, pod.Name, oldHost, multisite, out, errw)
		if err != nil {
			return err
		}
	}

	err = wp.Run(ctx, pod.Name, out, errw, "cache", "flush")
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Copied %s into %s as %s.\n", dst.site.Name, dst.env.Env.Name, dst.host)

	return nil
}

// moveHost runs search-replace and then the page builder fix-up.
func moveHost(
	ctx context.Context, wp *wpcli.Runner, dst side, pod, oldHost string, multisite bool, out, errw io.Writer,
) error {
	args := []string{"search-replace", oldHost, dst.host, "--all-tables-with-prefix", "--skip-columns=guid", "--precise"}
	if multisite {
		args = append(args, "--network")
	}

	err := wp.Run(ctx, pod, out, errw, args...)
	if err != nil {
		return err
	}

	local, cleanup, err := assets.WriteTemp(assets.RehostExtraName, assets.RehostExtra)
	if err != nil {
		return err
	}

	defer cleanup()

	err = dst.env.Kube.CopyFile(ctx, pod, local, rehostRemote)
	if err != nil {
		return err
	}

	return wp.Run(ctx, pod, out, errw, "eval-file", rehostRemote, oldHost, dst.host)
}
