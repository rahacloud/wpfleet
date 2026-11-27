// Package sites holds wpfleet sites: list, which shows every site of one or
// all environments, and show, which shows one site in detail.
package sites

import (
	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Cluster is what the sites commands read from an environment's cluster.
type Cluster = ops.Cluster

// Releases is what sites show needs from Helm.
type Releases = ops.ReleaseStatus

// deps are how the commands reach an environment's cluster and releases;
// nil uses the environment's own, and tests replace them with fakes.
type deps struct {
	cluster  func(env *cmdutil.Env) (Cluster, error)
	releases func(env *cmdutil.Env) (Releases, error)
}

// Command returns the wpfleet sites group.
func Command() *cli.Command {
	return command(deps{cluster: nil, releases: nil})
}

// command builds the group on d.
func command(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:     "sites",
		Usage:    "list the sites of the fleet and show one in detail",
		Commands: []*cli.Command{listCommand(d), showCommand(d)},
	}
}
