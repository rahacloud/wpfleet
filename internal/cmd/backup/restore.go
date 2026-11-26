package backup

import (
	"cmp"
	"context"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Flags of restore.
const (
	flagStamp   = "stamp"
	flagUploads = "uploads"
	flagYes     = "yes"
)

// stampLatest asks restore for the newest backup.
const stampLatest = "latest"

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

	env, site, v := t.env, t.site, t.values

	bucket, err := d.Bucket(env.Fleet)
	if err != nil {
		return err
	}

	prefix := domain.Prefix(env.Env.Namespace, site.Name)

	runs, err := bucket.Backups(ctx, prefix)
	if err != nil {
		return err
	}

	stamp, err := resolveStamp(cmd.String(flagStamp), runs)
	if err != nil {
		return fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	plan, err := domain.PlanRestore(prefix, runs, stamp, cmd.Bool(flagUploads))
	if err != nil {
		return fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	spec, err := d.Spec(env, v, plan, bucket.Bucket())
	if err != nil {
		return err
	}

	errw := cmd.Root().ErrWriter
	Describe(errw, env.Env.Name, spec)

	err = Confirm(cmd, cmd.Bool(flagYes), fmt.Sprintf("Overwrite %s in %s?", site.Name, env.Env.Name))
	if err != nil {
		return err
	}

	cluster, err := ClusterOf(env)
	if err != nil {
		return err
	}

	return d.Restore(ctx, cluster, spec, cmd.Root().Writer, errw)
}

// resolveStamp parses --stamp, where latest is the newest run with a dump.
func resolveStamp(s string, runs []domain.Backup) (domain.Stamp, error) {
	if s == stampLatest {
		b, err := domain.Latest(runs)

		return b.Stamp, err
	}

	return domain.ParseStamp(s)
}

// Spec builds the restore of plan into a site of env whose values (chart
// defaults included) are v, reading from bucket. The endpoint is the site's
// backup.s3.endpoint, the one its CronJob uses, else wpfleet.yaml's.
func (d Deps) Spec(env *cmdutil.Env, v values.Values, plan domain.RestorePlan, bucket string) (RestoreSpec, error) {
	site, err := domain.SiteOf(v)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("environment %s: %w", env.Env.Name, err)
	}

	tolerations, owner, err := PodOptions(v)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	return RestoreSpec{
		Name:         domain.JobName(site.FullName+"-restore", d.Now()),
		Site:         site,
		Plan:         plan,
		Bucket:       bucket,
		Endpoint:     cmp.Or(site.S3Endpoint, env.Config.Backup.S3.Endpoint),
		Tolerations:  tolerations,
		UploadsOwner: owner,
	}, nil
}

// Describe tells the user what a restore is about to do.
func Describe(w io.Writer, envName string, spec RestoreSpec) {
	what := "the database"
	if spec.Plan.Uploads {
		what = "the database and the uploads"
	}

	_, _ = fmt.Fprintf(w, "Restoring %s of %s in %s from s3://%s/%s at %s.\n",
		what, spec.Site.Name, envName, spec.Bucket, spec.Plan.Prefix, spec.Plan.Stamp)

	if !spec.Plan.Exact() {
		_, _ = fmt.Fprintf(w, "The uploads are the latest mirror wound back through %d later runs; "+
			"files first uploaded after %s stay.\n", len(spec.Plan.Overlays), spec.Plan.Stamp)
	}
}

// Restore creates the restore Job in the cluster and follows its logs into
// w until it ends; it fails when the Job fails.
func (d Deps) Restore(ctx context.Context, c Cluster, spec RestoreSpec, w, errw io.Writer) error {
	job, err := c.CreateJob(ctx, RestoreJob(spec))
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Started Job %s in %s.\n", job.Name, c.Namespace())

	err = Follow(ctx, c, job.Name, []string{ContainerFetch, ContainerImport}, w, d.Poll)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(errw, "Restored %s from %s.\n", spec.Site.Name, spec.Plan.Stamp)

	return nil
}
