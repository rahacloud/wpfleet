package admin

import (
	"context"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// NewTestCommand is the command with its cluster, browser and randomness
// replaced.
func NewTestCommand(
	pods kube.Pods, wp ops.WP, open func(context.Context, string) error, random io.Reader,
) *cli.Command {
	return newCommand(deps{
		connect: func(context.Context, *cmdutil.Env) (kube.Pods, ops.WP, error) { return pods, wp, nil },
		open:    open,
		random:  random,
	})
}
