package ops

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// AdminOptions are the options of AdminLogin.
type AdminOptions struct {
	// User is the login, email or ID to log in as; empty picks one.
	User string
	// MatchEmail, when User is empty, prefers the administrator with this
	// email: the server passes the signed-in person's email when
	// server.admin.matchEmail is on.
	MatchEmail string
	// Pods finds the site's running pod; nil uses the environment's cluster.
	Pods kube.Pods
	// WP runs wp-cli in it; nil copies wp-cli into the pod through the
	// environment's cluster, as wpfleet.yaml's wpcli says.
	WP WP
	// Random is the source of tokens; nil is crypto/rand.
	Random io.Reader
}

// AdminLogin makes a one-time login link to the admin of site in env, valid
// for LoginTTL, on the site's canonical URL from its values over the chart's
// defaults. The link is a credential until it expires: show it only to the
// person who asked for it.
func AdminLogin(ctx context.Context, env *Env, site fleet.Site, opts AdminOptions) (Login, error) {
	base, err := siteURL(env, site)
	if err != nil {
		return Login{}, err
	}

	pods, wp, err := adminConn(ctx, env, opts)
	if err != nil {
		return Login{}, err
	}

	pod, err := pods.RunningPod(ctx, site.Name)
	if err != nil {
		return Login{}, err
	}

	slog.DebugContext(ctx, "making a login link", "site", site.Name, "env", env.Env.Name, "pod", pod.Name)

	random := opts.Random
	if random == nil {
		random = rand.Reader
	}

	req := LoginRequest{Pod: pod.Name, BaseURL: base, User: opts.User, MatchEmail: opts.MatchEmail}

	login, err := IssueLogin(ctx, wp, req, random)
	if err != nil {
		return Login{}, fmt.Errorf("site %s: %w", site.Name, err)
	}

	return login, nil
}

// adminConn is the given pods and wp-cli, or the environment's.
//
//nolint:ireturn // callers depend on the interfaces so tests can fake them
func adminConn(ctx context.Context, env *Env, opts AdminOptions) (kube.Pods, WP, error) {
	pods := opts.Pods
	if pods == nil {
		pods = env.Kube
	}

	if opts.WP != nil {
		return pods, opts.WP, nil
	}

	runner, err := env.WPCLI(ctx, env.Kube)
	if err != nil {
		return nil, nil, err
	}

	return pods, runner, nil
}

// siteURL is the site's canonical URL from its values over the chart's
// defaults, so the host and the scheme are the ones the chart renders.
func siteURL(env *Env, site fleet.Site) (string, error) {
	v, err := env.ChartValues(site)
	if err != nil {
		return "", err
	}

	u, err := SiteURL(v)
	if err != nil {
		return "", fmt.Errorf("site %s: %w", site.Name, err)
	}

	return u, nil
}
