// Package cmdutil is what every command group shares: the global flags, the
// table and JSON output, and the loading of the fleet and of one environment.
// It is a package of its own so that the groups under internal/cmd can import
// it while internal/cmd imports them.
package cmdutil

import (
	"errors"

	"github.com/urfave/cli/v3"
)

// Names of the global flags.
const (
	FlagFleet   = "fleet"
	FlagEnv     = "env"
	FlagOutput  = "output"
	FlagVerbose = "verbose"
)

// ErrNoEnv is returned by commands that need --env when it is not given.
var ErrNoEnv = errors.New("this command needs an environment: --env or WPFLEET_ENV")

// GlobalFlags are the root command's flags, which every command sees.
func GlobalFlags() []cli.Flag {
	//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
	return []cli.Flag{
		&cli.StringFlag{
			Name:    FlagFleet,
			Aliases: []string{"f"},
			Usage:   "the fleet directory, which holds wpfleet.yaml",
			Value:   ".",
			Sources: cli.EnvVars("WPFLEET_FLEET"),
		},
		&cli.StringFlag{
			Name:    FlagEnv,
			Aliases: []string{"e"},
			Usage:   "the environment, as named in wpfleet.yaml",
			Sources: cli.EnvVars("WPFLEET_ENV"),
		},
		&cli.StringFlag{
			Name:    FlagOutput,
			Aliases: []string{"o"},
			Usage:   "output format: table or json",
			Value:   string(FormatTable),
			Sources: cli.EnvVars("WPFLEET_OUTPUT"),
			Validator: func(s string) error {
				_, err := ParseFormat(s)

				return err
			},
		},
		&cli.BoolFlag{
			Name:    FlagVerbose,
			Aliases: []string{"v"},
			Usage:   "log what wpfleet does, to stderr",
			Sources: cli.EnvVars("WPFLEET_VERBOSE"),
		},
	}
}
