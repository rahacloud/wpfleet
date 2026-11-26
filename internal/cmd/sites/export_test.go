package sites

import (
	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
)

// CommandWith builds the sites group on the given cluster and releases.
func CommandWith(
	cluster func(*cmdutil.Env) (Cluster, error), releases func(*cmdutil.Env) (Releases, error),
) *cli.Command {
	return command(deps{cluster: cluster, releases: releases})
}
