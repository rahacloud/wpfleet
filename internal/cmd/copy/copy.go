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
	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
	"github.com/rahacloud/wpfleet/internal/ops"
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

// side is one end of a copy: an environment, the site in it, its values over
// the chart's defaults, and its host and URL as the chart renders them.
type side struct {
	env    *cmdutil.Env
	site   fleet.Site
	values values.Values
	host   string
	url    string
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

	wp, err := c.ready(ctx, f, dst)
	if err != nil {
		return err
	}

	errw, out := cmd.Root().ErrWriter, cmd.Root().Writer

	backup.Describe(cmd, spec)
	_, _ = fmt.Fprintf(errw, "Then moving %s to %s.\n", src.host, dst.host)

	err = backup.Confirm(cmd, cmd.Bool(flagYes), fmt.Sprintf("Overwrite %s in %s with %s's?", name, to, from))
	if err != nil {
		return err
	}

	err = ops.Restore(ctx, dst.env, spec, c.d.Options(), cmdutil.ProgressTo(cmd))
	if err != nil {
		return err
	}

	return rehost(ctx, wp, src, dst, multisite, out, errw)
}

// ready checks, before anything is overwritten, that the target's cluster
// can create the restore Job, that the target has a running pod to move the
// host in and that wp-cli is at hand, and returns the runner.
func (c copier) ready(ctx context.Context, f *cmdutil.Fleet, dst side) (*wpcli.Runner, error) {
	cluster, err := ops.JobClusterOf(dst.env)
	if err != nil {
		return nil, err
	}

	_, err = cluster.RunningPod(ctx, dst.site.Name)
	if err != nil {
		return nil, err
	}

	return c.d.WPCLI(ctx, f, cluster)
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

	v, err := env.ChartValues(site)
	if err != nil {
		return side{}, err
	}

	host, err := fleet.Host(v)
	if err != nil {
		return side{}, fmt.Errorf("%s in %s: %w", name, envName, err)
	}

	u, err := ops.SiteURL(v)
	if err != nil {
		return side{}, fmt.Errorf("%s in %s: %w", name, envName, err)
	}

	return side{env: env, site: site, values: v, host: host, url: u}, nil
}

// plan finds the source's latest backup and builds its restore into the
// target, with the uploads when uploads is set. It also says whether the
// target is a multisite.
func (c copier) plan(
	ctx context.Context, f *cmdutil.Fleet, src, dst side, uploads bool,
) (ops.RestoreSpec, bool, error) {
	bucket, err := ops.Bucket(f, c.d.Getenv)
	if err != nil {
		return ops.RestoreSpec{}, false, err
	}

	prefix := domain.Prefix(src.env.Env.Namespace, src.site.Name)

	runs, err := bucket.Backups(ctx, prefix)
	if err != nil {
		return ops.RestoreSpec{}, false, err
	}

	latest, err := domain.Latest(runs)
	if err != nil {
		return ops.RestoreSpec{}, false, fmt.Errorf("%s in %s: %w", src.site.Name, src.env.Env.Name, err)
	}

	plan, err := domain.PlanRestore(prefix, runs, latest.Stamp, uploads)
	if err != nil {
		return ops.RestoreSpec{}, false, err
	}

	spec, err := ops.NewRestoreSpec(dst.env, dst.values, plan, bucket.Bucket(), c.d.Now())
	if err != nil {
		return ops.RestoreSpec{}, false, err
	}

	return spec, domain.Multisite(dst.values), nil
}

// rehost moves the restored site from the source's host to its own and
// flushes its cache, in a running pod of the target, looked up again because
// the restore may have taken a while.
func rehost(ctx context.Context, wp *wpcli.Runner, src, dst side, multisite bool, out, errw io.Writer) error {
	pod, err := dst.env.Kube.RunningPod(ctx, dst.site.Name)
	if err != nil {
		return err
	}

	if src.host == dst.host {
		_, _ = fmt.Fprintf(errw, "Both environments serve %s; no host to move.\n", src.host)
	} else {
		err = moveHost(ctx, wp, src, dst, pod.Name, multisite, out, errw)
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

// moveHost runs search-replace and then the page builder fix-up. On a
// multisite, the imported database still names the source's domain in
// wp_blogs and wp_site while the target's wp-config names its own, so wp-cli
// can only load WordPress for search-replace with --url set to the source's
// URL; once the domain is replaced, the target's own configuration works.
func moveHost(
	ctx context.Context, wp *wpcli.Runner, src, dst side, pod string, multisite bool, out, errw io.Writer,
) error {
	oldHost := src.host

	args := []string{"search-replace", oldHost, dst.host, "--all-tables-with-prefix", "--skip-columns=guid", "--precise"}
	if multisite {
		args = append(args, "--network", "--url="+src.url+"/")
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
