package build

import (
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/infra/docker"
)

// NewCommand builds the command on a docker runner and a clock the test
// gives.
func NewCommand(runner docker.Runner, now func() time.Time) *cli.Command {
	return newCommand(runner, now)
}
