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
	"log/slog"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// Flag names of the command.
const (
	flagUser = "user"
	flagOpen = "open"
	argSite  = "site"
)

// ErrNoSite is returned when the command gets no site.
var ErrNoSite = errors.New("name a site: wpfleet admin <site>")

// deps are what the command reaches the outside through, so that a test can
// replace them.
type deps struct {
	// connect returns the pods and wp-cli of the environment.
	connect func(ctx context.Context, env *cmdutil.Env) (conn, error)
	// open opens a URL in the user's browser.
	open func(ctx context.Context, url string) error
	// random is the source of tokens.
	random io.Reader
}

// Command is `wpfleet admin`.
func Command() *cli.Command {
	return newCommand(deps{connect: connect, open: Open, random: rand.Reader})
}

// conn is how the command reaches a site's pods.
type conn struct {
	// pods finds the site's running pod.
	pods kube.Pods
	// wp runs wp-cli in it.
	wp WP
}

// connect uses the environment's cluster and a wp-cli runner on it.
func connect(ctx context.Context, env *cmdutil.Env) (conn, error) {
	runner, err := env.WPCLI(ctx, env.Kube)
	if err != nil {
		return conn{pods: nil, wp: nil}, err
	}

	return conn{pods: env.Kube, wp: runner}, nil
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

	env, site, base, err := target(cmd, name)
	if err != nil {
		return err
	}

	c, err := d.connect(ctx, env)
	if err != nil {
		return err
	}

	pod, err := c.pods.RunningPod(ctx, site.Name)
	if err != nil {
		return err
	}

	slog.Debug("making a login link", "site", site.Name, "env", env.Env.Name, "pod", pod.Name)

	req := Request{Pod: pod.Name, BaseURL: base, User: cmd.String(flagUser)}

	login, err := Issue(ctx, c.wp, req, d.random)
	if err != nil {
		return fmt.Errorf("site %s: %w", site.Name, err)
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

// target opens the environment of --env and finds the site and its URL.
func target(cmd *cli.Command, name string) (*cmdutil.Env, fleet.Site, string, error) {
	env, err := cmdutil.OpenEnv(cmd)
	if err != nil {
		return nil, fleet.Site{}, "", err
	}

	site, err := env.Site(name)
	if err != nil {
		return nil, fleet.Site{}, "", err
	}

	base, err := siteURL(env, site)
	if err != nil {
		return nil, fleet.Site{}, "", err
	}

	return env, site, base, nil
}

// siteURL is the site's canonical URL from its values over the chart's
// defaults, so the host and the scheme are the ones the chart renders.
func siteURL(env *cmdutil.Env, site fleet.Site) (string, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return "", err
	}

	u, err := cmdutil.SiteURL(v)
	if err != nil {
		return "", fmt.Errorf("site %s: %w", site.Name, err)
	}

	return u, nil
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
