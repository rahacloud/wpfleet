package admin

import (
	"context"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// NewTestCommand is the command with its cluster, browser, clock and
// randomness replaced.
func NewTestCommand(
	pods kube.Pods, wp WP, open func(context.Context, string) error, random io.Reader,
) *cli.Command {
	return newCommand(deps{
		connect: func(context.Context, *cmdutil.Env) (conn, error) { return conn{pods: pods, wp: wp}, nil },
		open:    open,
		random:  random,
	})
}
