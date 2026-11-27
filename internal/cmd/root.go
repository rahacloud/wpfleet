// Package cmd is wpfleet's command tree: the root command with the global
// flags, and one group per package below it.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/carlmjohnson/versioninfo"
	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/admin"
	"github.com/rahacloud/wpfleet/internal/cmd/backup"
	"github.com/rahacloud/wpfleet/internal/cmd/build"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	copycmd "github.com/rahacloud/wpfleet/internal/cmd/copy"
	"github.com/rahacloud/wpfleet/internal/cmd/deploy"
	"github.com/rahacloud/wpfleet/internal/cmd/promote"
	"github.com/rahacloud/wpfleet/internal/cmd/serve"
	"github.com/rahacloud/wpfleet/internal/cmd/sites"
)

// New builds the root command. Output goes to stdout and errors to stderr;
// tests replace the command's Writer and ErrWriter.
func New() *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:                  "wpfleet",
		Usage:                 "manage a fleet of WordPress sites on Kubernetes",
		Version:               versioninfo.Short(),
		EnableShellCompletion: true,
		Suggest:               true,
		Flags:                 cmdutil.GlobalFlags(),
		Before:                setupLogging,
		Writer:                os.Stdout,
		ErrWriter:             os.Stderr,
		// Execute prints the error once; urfave/cli must not print or exit.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands: []*cli.Command{
			sites.Command(),
			deploy.Command(), deploy.DiffCommand(),
			build.Command(),
			promote.Command(),
			admin.Command(),
			backup.Command(),
			copycmd.Command(),
			serve.Command(),
		},
	}
}

// setupLogging sends debug logs to stderr with --verbose; without it only
// warnings and errors are logged.
func setupLogging(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	level := slog.LevelWarn
	if cmd.Bool(cmdutil.FlagVerbose) {
		level = slog.LevelDebug
	}

	//nolint:exhaustruct_v5 // a level is the only handler option needed
	slog.SetDefault(slog.New(slog.NewTextHandler(cmd.Root().ErrWriter, &slog.HandlerOptions{Level: level})))

	return ctx, nil
}

// Run runs the command tree on args and returns the exit code, printing a
// failure to stderr.
func Run(ctx context.Context, root *cli.Command, args []string) int {
	err := root.Run(ctx, args)
	if err == nil {
		return 0
	}

	return report(root.ErrWriter, err)
}

// report prints err and returns the exit code: the error's own when it
// carries one, else 1. An exit error with an empty message prints nothing.
func report(w io.Writer, err error) int {
	code := 1

	if exit, ok := errors.AsType[cli.ExitCoder](err); ok {
		code = exit.ExitCode()
	}

	if msg := err.Error(); msg != "" {
		_, _ = fmt.Fprintln(w, "wpfleet:", msg)
	}

	return code
}

// Execute runs wpfleet with the process's arguments and exits non-zero on
// failure. Ctrl-C cancels the context, so long operations stop cleanly.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	code := Run(ctx, New(), os.Args)

	stop()
	os.Exit(code)
}
