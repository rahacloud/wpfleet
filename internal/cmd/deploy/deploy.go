// Package deploy holds wpfleet deploy, which installs or upgrades the release
// of each site, and wpfleet diff, which shows what a deploy would change.
package deploy

import (
	"context"
	"strconv"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Flag names of deploy and diff.
const (
	flagAll  = "all"
	flagWait = "wait"
)

// ErrSitesFailed is returned when some of the sites of a deploy or a diff
// failed; the others were still handled.
var ErrSitesFailed = ops.ErrSitesFailed

// Releases is what deploy and diff need from Helm.
type Releases = ops.Releases

// releasesFunc gives the Releases of an environment.
type releasesFunc func(env *cmdutil.Env) (Releases, error)

// helmReleases is the Releases of an environment: its Helm client.
//
//nolint:ireturn // the commands depend on the interface so tests can fake Helm
func helmReleases(env *cmdutil.Env) (Releases, error) {
	h, err := env.Helm()
	if err != nil {
		return nil, err
	}

	return h, nil
}

// Command returns wpfleet deploy.
func Command() *cli.Command {
	return command(helmReleases)
}

// allFlag selects every site of the environment.
func allFlag() *cli.BoolFlag {
	//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
	return &cli.BoolFlag{Name: flagAll, Aliases: []string{"a"}, Usage: "every site of the environment"}
}

// command builds wpfleet deploy on the given Releases.
func command(releases releasesFunc) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "deploy",
		Usage:     "install or upgrade the release of each site (helm upgrade --install)",
		ArgsUsage: "<site>... | --all",
		Description: "Renders each site from the shared values file and its own, and installs the release, " +
			"or upgrades it with exactly those values. Secrets come from the site files as they are; " +
			"nothing is generated.",
		Flags: []cli.Flag{
			allFlag(),
			//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
			&cli.BoolFlag{Name: flagWait, Aliases: []string{"w"}, Usage: "wait until each site's resources are ready"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runDeploy(ctx, cmd, releases)
		},
	}
}

// Result is the outcome of deploying one site.
type Result = ops.DeployResult

// resultColumns are the columns of deploy's table.
func resultColumns() []cmdutil.Column[Result] {
	return []cmdutil.Column[Result]{
		{Header: "site", Value: func(r Result) string { return r.Site }},
		{Header: "env", Value: func(r Result) string { return r.Env }},
		{Header: "revision", Value: func(r Result) string { return revision(r.Revision) }},
		{Header: "status", Value: func(r Result) string { return r.Status }},
		{Header: "error", Value: func(r Result) string { return r.Error }},
	}
}

// revision formats a revision, leaving out the zero of a failed deploy.
func revision(n int) string {
	if n == 0 {
		return ""
	}

	return strconv.Itoa(n)
}

// runDeploy deploys the selected sites one after another. A failing site
// does not stop the others; the command fails at the end, naming them.
func runDeploy(ctx context.Context, cmd *cli.Command, releasesOf releasesFunc) error {
	env, sites, err := open(cmd)
	if err != nil {
		return err
	}

	releases, err := releasesOf(env)
	if err != nil {
		return err
	}

	opts := ops.DeployOptions{Wait: cmd.Bool(flagWait), Releases: releases, Commit: nil}

	report, err := ops.Deploy(ctx, env, sites, opts, cmdutil.ProgressTo(cmd))
	if err != nil {
		return err
	}

	err = cmdutil.Print(cmd, report.Results, resultColumns()...)
	if err != nil {
		return err
	}

	return report.Err()
}

// open opens the environment of --env and selects the sites the arguments
// name, or every site with --all.
func open(cmd *cli.Command) (*cmdutil.Env, []fleet.Site, error) {
	env, err := cmdutil.OpenEnv(cmd)
	if err != nil {
		return nil, nil, err
	}

	sites, err := env.SelectSites(cmd.Args().Slice(), cmd.Bool(flagAll))
	if err != nil {
		return nil, nil, err
	}

	return env, sites, nil
}
