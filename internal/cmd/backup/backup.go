// Package backup is the wpfleet backup command group: run a site's backup
// CronJob now, list a site's backups in S3, and restore one through a Job in
// the cluster. The restore is exported for wpfleet copy, which restores
// another environment's latest backup before it moves the host.
package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/s3"
	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
)

// defaultPoll is how often a command looks for a Job's pods and retries a
// log stream whose container has not started.
const defaultPoll = 2 * time.Second

// argSite names the site argument of the commands.
const argSite = "site"

// ErrNotConfirmed is returned when the user does not confirm a destructive
// step.
var ErrNotConfirmed = errors.New("not confirmed; pass --yes to skip the question")

// ErrNoJobCreator is returned when an environment's client cannot create
// Jobs that wpfleet builds itself.
var ErrNoJobCreator = errors.New("this cluster client cannot create Jobs")

// Deps are what the backup and copy commands reach outside the process.
// Command uses the real ones; tests put fakes in their place.
type Deps struct {
	// Open selects one environment of a loaded fleet.
	Open func(f *cmdutil.Fleet, env string) (*cmdutil.Env, error)
	// Getenv reads the S3 keys.
	Getenv func(key string) string
	// Now names Jobs.
	Now func() time.Time
	// Poll is how often Jobs' pods are looked for and log streams retried.
	Poll time.Duration
	// WPCLI returns a wp-cli runner for an environment's pods.
	WPCLI func(ctx context.Context, f *cmdutil.Fleet, k kube.Execer) (*wpcli.Runner, error)
}

// DefaultDeps are the real dependencies.
func DefaultDeps() Deps {
	return Deps{
		Open:   (*cmdutil.Fleet).Open,
		Getenv: os.Getenv,
		Now:    time.Now,
		Poll:   defaultPoll,
		WPCLI: func(ctx context.Context, f *cmdutil.Fleet, k kube.Execer) (*wpcli.Runner, error) {
			return f.WPCLI(ctx, k)
		},
	}
}

// Command is wpfleet backup.
func Command() *cli.Command {
	return NewCommand(DefaultDeps())
}

// NewCommand is wpfleet backup with the given dependencies.
func NewCommand(d Deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "backup",
		Usage: "run, list and restore a site's backups",
		Commands: []*cli.Command{
			runCommand(d),
			listCommand(d),
			restoreCommand(d),
		},
	}
}

// Cluster is what the restore needs from an environment's cluster: the
// commands' interface and the creation of a Job built in Go.
type Cluster interface {
	kube.Interface
	kube.JobCreator
}

// ClusterOf returns the environment's client as a Cluster.
//
//nolint:ireturn // the client is a fake in tests, so the caller gets the interface
func ClusterOf(env *cmdutil.Env) (Cluster, error) {
	c, ok := env.Kube.(Cluster)
	if !ok {
		return nil, fmt.Errorf("environment %s: %w", env.Env.Name, ErrNoJobCreator)
	}

	return c, nil
}

// OpenEnv loads the fleet and opens the environment of --env.
func (d Deps) OpenEnv(cmd *cli.Command) (*cmdutil.Env, error) {
	name := cmd.String(cmdutil.FlagEnv)
	if name == "" {
		return nil, cmdutil.ErrNoEnv
	}

	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return nil, err
	}

	return d.Open(f, name)
}

// Bucket returns a client for the fleet's backup bucket, with the keys from
// the environment.
func (d Deps) Bucket(f *cmdutil.Fleet) (*s3.Client, error) {
	creds, err := s3.CredentialsFromEnv(d.Getenv)
	if err != nil {
		return nil, err
	}

	cfg := f.Config.Backup.S3

	return s3.New(s3.Config{Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, Region: cfg.Region}, creds)
}

// target is a site of the environment of --env, with its values over the
// chart's defaults.
type target struct {
	env    *cmdutil.Env
	site   fleet.Site
	values values.Values
}

// target opens the environment of --env and loads the site of the argument.
func (d Deps) target(cmd *cli.Command) (target, error) {
	name, err := SiteArg(cmd)
	if err != nil {
		return target{}, err
	}

	env, err := d.OpenEnv(cmd)
	if err != nil {
		return target{}, err
	}

	site, err := env.Site(name)
	if err != nil {
		return target{}, err
	}

	v, err := ChartValues(env, site)
	if err != nil {
		return target{}, err
	}

	return target{env: env, site: site, values: v}, nil
}

// ChartValues returns a site's values over the chart's defaults, which is
// what the chart's templates see: the backup images and the S3 secret name
// usually come from the defaults.
func ChartValues(env *cmdutil.Env, site fleet.Site) (values.Values, error) {
	v, err := env.Values(site)
	if err != nil {
		return values.Values{}, err
	}

	h, err := env.Helm()
	if err != nil {
		return values.Values{}, err
	}

	return values.New(values.Merge(h.Chart().Values, v.Tree())), nil
}

// Confirm asks question on stderr and reads the answer from stdin, unless yes
// is set. Only y or yes goes ahead.
func Confirm(cmd *cli.Command, yes bool, question string) error {
	if yes {
		return nil
	}

	root := cmd.Root()
	if root.Reader == nil {
		return ErrNotConfirmed
	}

	_, _ = fmt.Fprintf(root.ErrWriter, "%s [y/N] ", question)

	line, err := bufio.NewReader(root.Reader).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading the answer: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return ErrNotConfirmed
	}
}

// siteArgument is the required site argument.
func siteArgument() []cli.Argument {
	//nolint:exhaustruct_v5 // urfave/cli arguments are configured by their set fields
	return []cli.Argument{&cli.StringArg{Name: argSite, UsageText: "<site>"}}
}

// ErrNoSite is returned when a command gets no site argument.
var ErrNoSite = errors.New("name a site")

// SiteArg returns the site argument, which must be given.
func SiteArg(cmd *cli.Command) (string, error) {
	name := cmd.StringArg(argSite)
	if name == "" {
		return "", ErrNoSite
	}

	return name, nil
}

// SiteArgument is the site argument, for copy.
func SiteArgument() []cli.Argument {
	return siteArgument()
}
