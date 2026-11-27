package sites

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Row is one site in one environment, as sites list prints it.
type Row = ops.SiteRow

// rowColumns are the columns of sites list.
func rowColumns() []cmdutil.Column[Row] {
	return []cmdutil.Column[Row]{
		{Header: "site", Value: func(r Row) string { return r.Site }},
		{Header: "env", Value: func(r Row) string { return r.Env }},
		{Header: "host", Value: func(r Row) string { return r.Host }},
		{Header: "image", Value: func(r Row) string { return r.Image }},
		{Header: "ready", Value: func(r Row) string { return r.Ready }},
	}
}

// listCommand builds wpfleet sites list.
func listCommand(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "list",
		Usage: "every site of --env, or of every environment without it: host, image and pod readiness",
		Description: "Host and image come from the values files; READY counts the site's ready pods out of " +
			"its running ones. A cluster that cannot be reached shows READY as unknown and is logged; " +
			"the rest of the list still prints.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runList(ctx, cmd, d)
		},
	}
}

// runList lists the sites of the selected environments, in the order of
// wpfleet.yaml, each environment's sites by name.
func runList(ctx context.Context, cmd *cli.Command, d deps) error {
	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	envs := f.Fleet.Environments

	if name := cmd.String(cmdutil.FlagEnv); name != "" {
		env, err := f.Fleet.Environment(name)
		if err != nil {
			return err
		}

		envs = []fleet.Environment{env}
	}

	rows, err := ops.ListSites(ctx, f, envs, ops.ListOptions{Cluster: d.cluster})
	if err != nil {
		return err
	}

	return cmdutil.Print(cmd, rows, rowColumns()...)
}
