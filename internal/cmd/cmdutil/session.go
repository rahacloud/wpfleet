package cmdutil

import (
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/ops"
)

// Fleet is a loaded fleet repository; the server loads the same type.
type Fleet = ops.Fleet

// Env is the fleet with one environment selected; the server opens the same
// type.
type Env = ops.Env

// LoadFleet loads wpfleet.yaml from --fleet.
func LoadFleet(cmd *cli.Command) (*Fleet, error) {
	return ops.LoadFleet(cmd.String(FlagFleet))
}

// OpenEnv loads the fleet and selects the environment of --env, discovering
// its sites and making its kube client without contacting the cluster.
func OpenEnv(cmd *cli.Command) (*Env, error) {
	name := cmd.String(FlagEnv)
	if name == "" {
		return nil, ErrNoEnv
	}

	f, err := LoadFleet(cmd)
	if err != nil {
		return nil, err
	}

	return f.Open(name)
}

// ProgressTo prints an operation's progress the way the commands always
// have: Job logs on stdout as they come, and every other event's line on
// stderr, so that stdout keeps the result.
func ProgressTo(cmd *cli.Command) ops.Progress {
	root := cmd.Root()

	return func(e ops.Event) {
		if e.Kind == ops.EventLog {
			_, _ = fmt.Fprint(root.Writer, e.Text)

			return
		}

		if e.Text != "" {
			_, _ = fmt.Fprintln(root.ErrWriter, e.Text)
		}
	}
}
