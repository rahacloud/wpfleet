// Package serve holds wpfleet serve: the web UI and its single sign-on over
// the same operations as the CLI. It reads the server section of a
// wpfleet.yaml and the secrets from the environment, clones the fleet
// repository, and wires the oidc, session, access, gitrepo and audit
// packages into the web server.
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/oidc"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// The flags of wpfleet serve.
const (
	flagConfig          = "config"
	flagData            = "data"
	flagShutdownTimeout = "shutdown-timeout"
)

// The HTTP servers' timeouts. There is no write timeout: the progress of an
// action streams over server-sent events for as long as it runs.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = time.Minute
	idleTimeout       = 2 * time.Minute
)

// defaultShutdownTimeout is how long a stop waits for running actions by
// default: long enough for a deploy that waits for readiness.
const defaultShutdownTimeout = 5 * time.Minute

// The directories under --data.
const (
	cloneDir     = "fleet"
	snapshotsDir = "snapshots"
)

// ErrNoAccess is returned when the server section has no access rule: every
// request would be refused, which is a mistake, not a configuration.
var ErrNoAccess = errors.New("server.access has no rule, so nobody could use the server")

// Command returns wpfleet serve.
func Command() *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "serve",
		Usage: "run the web UI, with single sign-on, over the fleet repository",
		Description: "serve clones the fleet repository named in the server section of --config, keeps the clone " +
			"at the branch's head, and serves the UI on server.listen and /metrics on server.metricsListen. " +
			"Secrets come only from the environment: WPFLEET_OIDC_CLIENT_SECRET, WPFLEET_SESSION_KEY, " +
			"WPFLEET_GIT_SSH_KEY or WPFLEET_GIT_TOKEN, and WPFLEET_GIT_WEBHOOK_SECRET.",
		//nolint:exhaustruct_v5 // urfave/cli flags are configured by their set fields
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     flagConfig,
				Usage:    "the server's wpfleet.yaml, with the server section",
				Required: true,
				Sources:  cli.EnvVars("WPFLEET_CONFIG"),
			},
			&cli.StringFlag{
				Name:    flagData,
				Usage:   "the directory for the clone of the fleet repository and its snapshots",
				Value:   "/var/lib/wpfleet",
				Sources: cli.EnvVars("WPFLEET_DATA"),
			},
			&cli.DurationFlag{
				Name: flagShutdownTimeout,
				Usage: "how long a stop waits for running actions; a deploy cut short leaves its " +
					"release pending",
				Value: defaultShutdownTimeout,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			level := slog.LevelInfo
			if cmd.Bool(cmdutil.FlagVerbose) {
				level = slog.LevelDebug
			}

			//nolint:exhaustruct_v5 // a level is the only handler option needed
			log := slog.New(slog.NewJSONHandler(cmd.Root().Writer, &slog.HandlerOptions{Level: level}))
			slog.SetDefault(log)

			return run(ctx, log, options{
				config:          cmd.String(flagConfig),
				data:            cmd.String(flagData),
				shutdownTimeout: cmd.Duration(flagShutdownTimeout),
				getenv:          os.Getenv,
				version:         cmd.Root().Version,
				ready:           nil,
			})
		},
	}
}

// options are what run needs besides the logger.
type options struct {
	config          string
	data            string
	shutdownTimeout time.Duration
	getenv          func(string) string
	// version is wpfleet's version, for the footer of the UI.
	version string
	// ready, when set, gets the addresses the servers listen on once they
	// do; tests listen on port 0 and read it.
	ready func(listen, metrics net.Addr)
}

// run starts the server and serves until ctx ends, then shuts down.
func run(ctx context.Context, log *slog.Logger, o options) error {
	srv, policy, secrets, err := load(o)
	if err != nil {
		return err
	}

	fleetClone, err := openClone(ctx, log, o.data, srv.Git, secrets, policy)
	if err != nil {
		return err
	}

	err = checkBranding(fleetClone.Dir(), srv.Branding)
	if err != nil {
		return err
	}

	provider, err := oidc.New(ctx, oidc.Config{
		Issuer: srv.OIDC.Issuer, ClientID: srv.OIDC.ClientID, ClientSecret: secrets.OIDCClientSecret,
		RedirectURL: srv.RedirectURL(), GroupsClaim: srv.OIDC.GroupsClaim, Scopes: srv.OIDC.Scopes,
		AssumeEmailVerified: srv.OIDC.AssumeEmailVerified,
	})
	if err != nil {
		return err
	}

	secure := strings.HasPrefix(srv.PublicURL, "https://")

	sessions, err := session.NewManager(secrets.SessionKey, srv.Session.MaxAge, secure)
	if err != nil {
		return err
	}

	instance, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("reading the host name: %w", err)
	}

	auditor := newAuditor(log, fleetCluster(fleetClone.Dir), instance)

	handler, err := web.New(web.Deps{
		Auth:          ruleGroups(provider, policy),
		Sessions:      sessions,
		Access:        policy,
		Repo:          fleetClone,
		Ops:           web.FleetOps{Dir: fleetClone.Dir, Getenv: o.getenv},
		Locks:         new(ops.Locks),
		Audit:         auditor.write,
		Logger:        log,
		MatchEmail:    srv.Admin.MatchEmail,
		Committer:     gitrepo.Signature{Name: srv.Git.Committer.Name, Email: srv.Git.Committer.Email},
		VerifyWebhook: webhookVerifier(secrets.GitWebhookSecret),
		Now:           nil,
		Branding:      branding(srv.Branding, fleetClone.Dir),
		Version:       o.version,
	})
	if err != nil {
		return err
	}

	return serve(ctx, log, o, srv, handler, auditor)
}

// openClone clones the fleet repository under data, snapshots it and checks
// that the fleet it holds loads and has the environments the rules name.
func openClone(
	ctx context.Context, log *slog.Logger, data string, git config.ServerGit, secrets config.ServerSecrets,
	policy access.Policy,
) (*clone, error) {
	log.InfoContext(ctx, "cloning the fleet repository", "url", git.URL, "branch", git.Branch)

	repo, err := gitrepo.Open(ctx, gitrepo.Config{
		URL: git.URL, Branch: git.Branch, Dir: filepath.Join(data, cloneDir),
		SSHKeyFile: secrets.GitSSHKeyFile, Token: secrets.GitToken,
		Committer: gitrepo.Signature{Name: git.Committer.Name, Email: git.Committer.Email},
	})
	if err != nil {
		return nil, fmt.Errorf("opening the fleet repository: %w", err)
	}

	c, err := newClone(repo, filepath.Join(data, snapshotsDir), log)
	if err != nil {
		return nil, err
	}

	return c, checkFleet(c.Dir(), policy)
}

// load reads the server section and the secrets and builds the access
// policy, refusing what serve cannot start with.
func load(o options) (config.Server, access.Policy, config.ServerSecrets, error) {
	var (
		none    config.Server
		noRules access.Policy
		nothing config.ServerSecrets
	)

	cfg, err := config.LoadFile(o.config, nil)
	if err != nil {
		return none, noRules, nothing, err
	}

	policy, err := accessPolicy(cfg.Server.Access)
	if err != nil {
		return none, noRules, nothing, err
	}

	err = cfg.ValidateServer()
	if err != nil {
		return none, noRules, nothing, err
	}

	secrets, err := config.ServerSecretsFromEnv(o.getenv)
	if err != nil {
		return none, noRules, nothing, err
	}

	return cfg.Server, policy, secrets, nil
}

// serve listens, polls the repository, and shuts everything down in order
// when ctx ends or a listener fails: the listeners first, then the running
// actions, then the Events they recorded.
func serve(
	ctx context.Context, log *slog.Logger, o options, srv config.Server, handler *web.Server, auditor *auditor,
) error {
	ui, metrics, err := listenBoth(ctx, srv, handler)
	if err != nil {
		return err
	}

	if o.ready != nil {
		o.ready(ui.addr, metrics.addr)
	}

	log.InfoContext(ctx, "serving", "listen", ui.addr.String(), "metrics", metrics.addr.String(),
		"publicURL", srv.PublicURL)

	failed := make(chan error, len([]*listener{ui, metrics}))

	go func() { failed <- ui.serve() }()

	if metrics != ui {
		go func() { failed <- metrics.serve() }()
	}

	pollCtx, stopPolling := context.WithCancel(ctx)
	polled := make(chan struct{})

	go func() {
		defer close(polled)
		poll(pollCtx, srv.Git.PollInterval, handler.TriggerSync)
	}()

	var serveErr error

	select {
	case <-ctx.Done():
		log.InfoContext(ctx, "stopping")
	case serveErr = <-failed:
		log.ErrorContext(ctx, "a listener failed; stopping", "error", serveErr)
	}

	stopPolling()
	<-polled

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.shutdownTimeout)
	defer cancel()

	errs := []error{serveErr, ui.shutdown(shutdownCtx)}
	if metrics != ui {
		errs = append(errs, metrics.shutdown(shutdownCtx))
	}

	errs = append(errs, handler.Shutdown(shutdownCtx))
	auditor.wait()

	return errors.Join(errs...)
}

// listenBoth opens the UI's listener and the metrics' one, which is the
// UI's own when server.metricsListen is empty.
func listenBoth(ctx context.Context, srv config.Server, handler *web.Server) (*listener, *listener, error) {
	ui, err := listen(ctx, srv.Listen, handler)
	if err != nil {
		return nil, nil, err
	}

	if srv.MetricsListen == "" {
		return ui, ui, nil
	}

	metrics, err := listen(ctx, srv.MetricsListen, handler.MetricsHandler())
	if err != nil {
		return nil, nil, errors.Join(err, ui.Close())
	}

	return ui, metrics, nil
}

// poll calls sync every interval until ctx ends.
func poll(ctx context.Context, interval time.Duration, sync func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

// listener is one HTTP server on its own listener.
type listener struct {
	server *http.Server
	ln     net.Listener
	addr   net.Addr
}

// listen opens addr for h.
func listen(ctx context.Context, addr string, h http.Handler) (*listener, error) {
	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}

	//nolint:exhaustruct_v5 // the other fields are net/http's defaults
	server := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}

	return &listener{server: server, ln: ln, addr: ln.Addr()}, nil
}

// Close closes the listener of a server that never served.
func (l *listener) Close() error {
	err := l.ln.Close()
	if err != nil {
		return fmt.Errorf("closing %s: %w", l.addr, err)
	}

	return nil
}

// serve serves until shutdown; a shutdown is not an error.
func (l *listener) serve() error {
	err := l.server.Serve(l.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return fmt.Errorf("serving on %s: %w", l.addr, err)
}

// shutdown stops accepting and waits for the open requests. Streams of
// server-sent events do not end by themselves, so when ctx ends first the
// connections are closed.
func (l *listener) shutdown(ctx context.Context) error {
	err := l.server.Shutdown(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("stopping %s: %w", l.addr, err), l.server.Close())
	}

	return nil
}

// accessPolicy turns the server section's rules into the access policy and
// validates it; no rule at all is refused.
func accessPolicy(rules []config.AccessRule) (access.Policy, error) {
	if len(rules) == 0 {
		return access.Policy{}, ErrNoAccess
	}

	policy := access.Policy{Rules: make([]access.Rule, 0, len(rules))}

	for _, r := range rules {
		allow := make([]access.Permission, 0, len(r.Allow))
		for _, p := range r.Allow {
			allow = append(allow, access.Permission(p))
		}

		policy.Rules = append(policy.Rules, access.Rule{
			Group: r.Group, Environments: r.Environments, Sites: r.Sites, Allow: allow,
		})
	}

	err := policy.Validate()
	if err != nil {
		return access.Policy{}, fmt.Errorf("server.access: %w", err)
	}

	return policy, nil
}

// checkFleet loads the fleet from the clone and refuses a start where an
// access rule names an environment the fleet does not have: the rule could
// never match, and a typo there hides sites from the people it was for.
func checkFleet(dir string, policy access.Policy) error {
	f, err := ops.LoadFleet(dir)
	if err != nil {
		return fmt.Errorf("loading the fleet from the repository: %w", err)
	}

	for i, r := range policy.Rules {
		for _, env := range r.Environments {
			if env == access.Wildcard {
				continue
			}

			_, err := f.Fleet.Environment(env)
			if err != nil {
				return fmt.Errorf("server.access[%d]: the fleet repository has no environment %q: %w", i, env, err)
			}
		}
	}

	return nil
}

// checkBranding refuses a start whose logo or favicon, in the fleet at
// dir, is missing, too large or not the type its name says. After a push
// the server leaves a broken one out and logs why instead, so a bad commit
// never takes the pages down.
func checkBranding(dir string, b config.Branding) error {
	for _, f := range []struct{ key, path string }{
		{"server.branding.logo", b.Logo}, {"server.branding.favicon", b.Favicon},
	} {
		if f.path == "" {
			continue
		}

		_, err := web.LoadBrandImage(dir, f.path)
		if err != nil {
			return fmt.Errorf("%s in the fleet repository: %w", f.key, err)
		}
	}

	return nil
}

// branding turns the server section's branding into the web server's,
// reading the images from the newest snapshot of the fleet.
func branding(b config.Branding, dir func() string) web.Branding {
	return web.Branding{
		Name: b.Name, Logo: b.Logo, Favicon: b.Favicon, Dir: dir, PrimaryColor: b.PrimaryColor,
		SupportURL: b.SupportURL, SupportEmail: b.SupportEmail,
		Welcome: map[i18n.Lang]string{i18n.English: b.Welcome.EN, i18n.Persian: b.Welcome.FA},
	}
}

// webhookVerifier checks a push webhook against secret; with no secret the
// webhook is off and only polling syncs the clone.
func webhookVerifier(secret []byte) func(body []byte, signature string) bool {
	if len(secret) == 0 {
		return nil
	}

	return func(body []byte, signature string) bool {
		return gitrepo.VerifyWebhook(secret, body, signature)
	}
}

// Interface checks: the real packages are what web consumes.
var (
	_ web.Sessions      = (*session.Manager)(nil)
	_ web.Authorizer    = access.Policy{Rules: nil}
	_ web.Repo          = (*clone)(nil)
	_ web.Authenticator = groupFilter{Provider: nil, named: nil}
	_ io.Closer         = (*listener)(nil)
)
