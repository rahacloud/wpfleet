package ops

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	domain "github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/s3"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// defaultPoll is how often a Job's pods are looked for and a log stream
// whose container has not started is retried.
const defaultPoll = 2 * time.Second

// StampLatest asks a restore for the newest backup.
const StampLatest = "latest"

// ErrNoJobCreator is returned when an environment's client cannot create
// Jobs that wpfleet builds itself.
var ErrNoJobCreator = errors.New("this cluster client cannot create Jobs")

// BackupOptions are what the backup operations reach outside the process.
// The zero value uses the real ones.
type BackupOptions struct {
	// Now names Jobs; nil is time.Now.
	Now func() time.Time
	// Poll is how often Jobs' pods are looked for and log streams retried;
	// zero is two seconds.
	Poll time.Duration
	// Getenv reads the S3 keys; nil is os.Getenv.
	Getenv func(key string) string
}

// now is the options' clock.
func (o BackupOptions) now() time.Time {
	if o.Now == nil {
		return time.Now()
	}

	return o.Now()
}

// poll is the options' poll interval.
func (o BackupOptions) poll() time.Duration {
	return cmp.Or(o.Poll, defaultPoll)
}

// getenv is the options' environment.
func (o BackupOptions) getenv() func(string) string {
	if o.Getenv == nil {
		return os.Getenv
	}

	return o.Getenv
}

// JobCluster is what a restore needs from an environment's cluster: the
// operations' interface and the creation of a Job built in Go.
type JobCluster interface {
	kube.Interface
	kube.JobCreator
}

// JobClusterOf returns the environment's client as a JobCluster.
//
//nolint:ireturn // the client is a fake in tests, so the caller gets the interface
func JobClusterOf(env *Env) (JobCluster, error) {
	c, ok := env.Kube.(JobCluster)
	if !ok {
		return nil, fmt.Errorf("environment %s: %w", env.Env.Name, ErrNoJobCreator)
	}

	return c, nil
}

// Bucket returns a client for the fleet's backup bucket, with the keys from
// getenv (WPFLEET_S3_ACCESS_KEY_ID and WPFLEET_S3_SECRET_ACCESS_KEY).
func Bucket(f *Fleet, getenv func(string) string) (*s3.Client, error) {
	creds, err := s3.CredentialsFromEnv(getenv)
	if err != nil {
		return nil, err
	}

	cfg := f.Config.Backup.S3

	return s3.New(s3.Config{Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, Region: cfg.Region}, creds)
}

// BackupRun is a backup that ran.
type BackupRun struct {
	// Job is the Job's name.
	Job string `json:"job"`
	// Namespace is its namespace.
	Namespace string `json:"namespace"`
}

// RunBackup starts a Job from the site's backup CronJob, streams its logs as
// EventLog events until it ends, and fails (wrapping kube.ErrJobFailed) when
// it fails.
func RunBackup(
	ctx context.Context, env *Env, site fleet.Site, opts BackupOptions, progress Progress,
) (BackupRun, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return BackupRun{}, err
	}

	s, err := domain.SiteOf(v)
	if err != nil {
		return BackupRun{}, fmt.Errorf("environment %s: %w", env.Env.Name, err)
	}

	cronJob := s.FullName + "-backup"

	job, err := env.Kube.JobFromCronJob(ctx, cronJob, domain.JobName(cronJob, opts.now()))
	if err != nil {
		return BackupRun{}, fmt.Errorf("%s in %s (is backup.enabled set?): %w", site.Name, env.Env.Name, err)
	}

	run := BackupRun{Job: job.Name, Namespace: env.Kube.Namespace()}

	progress.emit(Event{
		Kind: EventJobStarted, Env: env.Env.Name, Site: site.Name, Job: run.Job, Namespace: run.Namespace,
		Text: fmt.Sprintf("Started Job %s in %s.", run.Job, run.Namespace),
	})

	logs := newLogWriter(progress, env.Env.Name, site.Name, run.Job)
	containers := []string{ContainerDump, ContainerUpload}

	err = Follow(ctx, env.Kube, run.Job, containers, logs, opts.poll())
	if err != nil {
		return run, err
	}

	progress.emit(Event{
		Kind: EventDone, Env: env.Env.Name, Site: site.Name, Job: run.Job, Namespace: run.Namespace,
		Text: fmt.Sprintf("Backed up %s in %s.", site.Name, env.Env.Name),
	})

	return run, nil
}

// BackupRow is one backup of a site in S3.
type BackupRow struct {
	domain.Backup

	// Expires is when the CronJob deletes the run, by backup.keepDays.
	Expires time.Time `json:"expires"`
}

// ListBackups lists the backups of a site in S3, newest first, with when the
// CronJob's retention deletes each. It needs the S3 keys.
func ListBackups(ctx context.Context, env *Env, site fleet.Site, opts BackupOptions) ([]BackupRow, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return nil, err
	}

	keep, err := strconv.Atoi(v.String(domain.ValueKeepDays))
	if err != nil {
		return nil, fmt.Errorf("%s in %s: %s: %w", site.Name, env.Env.Name, domain.ValueKeepDays, err)
	}

	bucket, err := Bucket(env.Fleet, opts.getenv())
	if err != nil {
		return nil, err
	}

	runs, err := bucket.Backups(ctx, domain.Prefix(env.Env.Namespace, site.Name))
	if err != nil {
		return nil, err
	}

	rows := make([]BackupRow, 0, len(runs))
	for _, b := range runs {
		rows = append(rows, BackupRow{Backup: b, Expires: domain.Expires(b.Stamp, keep)})
	}

	return rows, nil
}

// RestoreOptions are the options of PlanRestore.
type RestoreOptions struct {
	BackupOptions

	// Stamp is the backup to restore, as ListBackups gives it, or
	// StampLatest.
	Stamp string
	// Uploads restores the uploads too.
	Uploads bool
}

// PlanRestore finds the backup and builds the restore Job's spec, without
// creating anything. With uploads it checks that the site has a running pod,
// which holds the volume the Job must run beside. DescribeRestore says what
// the spec does; Restore runs it.
func PlanRestore(ctx context.Context, env *Env, site fleet.Site, opts RestoreOptions) (RestoreSpec, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return RestoreSpec{}, err
	}

	bucket, err := Bucket(env.Fleet, opts.getenv())
	if err != nil {
		return RestoreSpec{}, err
	}

	prefix := domain.Prefix(env.Env.Namespace, site.Name)

	runs, err := bucket.Backups(ctx, prefix)
	if err != nil {
		return RestoreSpec{}, err
	}

	stamp, err := resolveStamp(opts.Stamp, runs)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	plan, err := domain.PlanRestore(prefix, runs, stamp, opts.Uploads)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	spec, err := NewRestoreSpec(env, v, plan, bucket.Bucket(), opts.now())
	if err != nil {
		return RestoreSpec{}, err
	}

	cluster, err := JobClusterOf(env)
	if err != nil {
		return RestoreSpec{}, err
	}

	err = besidePod(ctx, cluster, plan, site.Name)
	if err != nil {
		return RestoreSpec{}, err
	}

	return spec, nil
}

// besidePod checks that a restore of the uploads has the site's running pod
// to run beside: the pod holds the volume, and without one the Job would wait
// until its deadline.
func besidePod(ctx context.Context, cluster kube.Pods, plan domain.RestorePlan, site string) error {
	if !plan.Uploads {
		return nil
	}

	_, err := cluster.RunningPod(ctx, site)
	if err != nil {
		return fmt.Errorf("restoring the uploads runs beside the site's pod: %w", err)
	}

	return nil
}

// resolveStamp parses a stamp, where StampLatest is the newest run with a
// dump.
func resolveStamp(s string, runs []domain.Backup) (domain.Stamp, error) {
	if s == StampLatest {
		b, err := domain.Latest(runs)

		return b.Stamp, err
	}

	return domain.ParseStamp(s)
}

// NewRestoreSpec builds the restore of plan into a site of env whose values
// (chart defaults included) are v, reading from bucket, with the Job named
// at now. The endpoint is the site's backup.s3.endpoint, the one its CronJob
// uses, else wpfleet.yaml's.
func NewRestoreSpec(
	env *Env, v values.Values, plan domain.RestorePlan, bucket string, now time.Time,
) (RestoreSpec, error) {
	site, err := domain.SiteOf(v)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("environment %s: %w", env.Env.Name, err)
	}

	tolerations, owner, err := PodOptions(v)
	if err != nil {
		return RestoreSpec{}, fmt.Errorf("%s in %s: %w", site.Name, env.Env.Name, err)
	}

	return RestoreSpec{
		Name:         domain.JobName(site.FullName+"-restore", now),
		Env:          env.Env.Name,
		Site:         site,
		Plan:         plan,
		Bucket:       bucket,
		Endpoint:     cmp.Or(site.S3Endpoint, env.Config.Backup.S3.Endpoint),
		Tolerations:  tolerations,
		UploadsOwner: owner,
	}, nil
}

// DescribeRestore says in English what a restore is about to do, one line
// per sentence.
func DescribeRestore(spec RestoreSpec) []string {
	what := "the database"
	if spec.Plan.Uploads {
		what = "the database and the uploads"
	}

	lines := []string{fmt.Sprintf("Restoring %s of %s in %s from s3://%s/%s at %s.",
		what, spec.Site.Name, spec.Env, spec.Bucket, spec.Plan.Prefix, spec.Plan.Stamp)}

	if !spec.Plan.Exact() {
		lines = append(lines, fmt.Sprintf("The uploads are the latest mirror wound back through %d later runs; "+
			"files first uploaded after %s stay.", len(spec.Plan.Overlays), spec.Plan.Stamp))
	}

	return lines
}

// Restore creates the restore Job of spec in env's cluster and streams its
// logs as EventLog events until it ends; it fails (wrapping
// kube.ErrJobFailed) when the Job fails. It overwrites the site's database,
// and with the plan's uploads its uploads: callers ask first.
func Restore(ctx context.Context, env *Env, spec RestoreSpec, opts BackupOptions, progress Progress) error {
	c, err := JobClusterOf(env)
	if err != nil {
		return err
	}

	job, err := c.CreateJob(ctx, RestoreJob(spec))
	if err != nil {
		return err
	}

	progress.emit(Event{
		Kind: EventJobStarted, Env: spec.Env, Site: spec.Site.Name, Job: job.Name, Namespace: c.Namespace(),
		Text: fmt.Sprintf("Started Job %s in %s.", job.Name, c.Namespace()),
	})

	logs := newLogWriter(progress, spec.Env, spec.Site.Name, job.Name)

	err = Follow(ctx, c, job.Name, []string{ContainerFetch, ContainerImport}, logs, opts.poll())
	if err != nil {
		return err
	}

	progress.emit(Event{
		Kind: EventDone, Env: spec.Env, Site: spec.Site.Name, Job: job.Name, Namespace: c.Namespace(),
		Text: fmt.Sprintf("Restored %s from %s.", spec.Site.Name, spec.Plan.Stamp),
	})

	return nil
}
