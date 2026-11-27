// Package admin is `wpfleet admin`: a one-time login link to a site's admin,
// answered by the must-use plugin that the chart mounts when
// wordpress.oneTimeLogin is on.
package admin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Flag names of the command.
const (
	flagUser = "user"
	flagOpen = "open"
	argSite  = "site"
)

// ErrNoSite is returned when the command gets no site.
var ErrNoSite = errors.New("name a site: wpfleet admin <site>")

// Login is a link that was made.
type Login = ops.Login

// deps are what the command reaches the outside through, so that a test can
// replace them.
type deps struct {
	// connect returns the pods and wp-cli to use; nil uses the
	// environment's cluster and a wp-cli runner on it.
	connect func(ctx context.Context, env *cmdutil.Env) (kube.Pods, ops.WP, error)
	// open opens a URL in the user's browser.
	open func(ctx context.Context, url string) error
	// random is the source of tokens.
	random io.Reader
}

// Command is `wpfleet admin`.
func Command() *cli.Command {
	return newCommand(deps{connect: nil, open: Open, random: rand.Reader})
}

func newCommand(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "admin",
		Usage:     "print a one-time login link to a site's admin, valid for 60 seconds",
		ArgsUsage: "<site>",
		Arguments: []cli.Argument{
			//nolint:exhaustruct_v5 // urfave/cli arguments are configured by their set fields
			&cli.StringArg{Name: argSite, UsageText: "the site to log in to"},
		},
		Flags: []cli.Flag{
			//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
			&cli.StringFlag{
				Name:  flagUser,
				Usage: "log in as this user (login, email or ID); default: the first administrator, or super admin on a multisite",
			},
			//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
			&cli.BoolFlag{Name: flagOpen, Usage: "open the link in the browser"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(ctx, cmd, d)
		},
	}
}

// run makes the link and prints or opens it.
func run(ctx context.Context, cmd *cli.Command, d deps) error {
	name := cmd.StringArg(argSite)
	if name == "" {
		return ErrNoSite
	}

	format, err := cmdutil.ParseFormat(cmd.String(cmdutil.FlagOutput))
	if err != nil {
		return err
	}

	env, err := cmdutil.OpenEnv(cmd)
	if err != nil {
		return err
	}

	site, err := env.Site(name)
	if err != nil {
		return err
	}

	opts := ops.AdminOptions{User: cmd.String(flagUser), MatchEmail: "", Pods: nil, WP: nil, Random: d.random}

	if d.connect != nil {
		opts.Pods, opts.WP, err = d.connect(ctx, env)
		if err != nil {
			return err
		}
	}

	login, err := ops.AdminLogin(ctx, env, site, opts)
	if err != nil {
		return err
	}

	err = writeLogin(cmd.Root().Writer, format, login)
	if err != nil {
		return err
	}

	if cmd.Bool(flagOpen) {
		return d.open(ctx, login.URL)
	}

	return nil
}

// writeLogin writes the link alone for people, so that it can be copied, or the
// whole login as JSON for scripts.
func writeLogin(w io.Writer, format cmdutil.Format, login Login) error {
	if format == cmdutil.FormatJSON {
		return cmdutil.Write(w, format, []Login{login})
	}

	_, err := fmt.Fprintln(w, login.URL)
	if err != nil {
		return fmt.Errorf("printing the link: %w", err)
	}

	return nil
}
