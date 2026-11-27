package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// Permission is what a rule allows on a site. Every permission implies
// PermView.
type Permission string

// The permissions of docs/design.md.
const (
	// PermView shows the site, its details, diff, history and backups.
	PermView Permission = "view"
	// PermDeploy allows promote into the environment and deploy there.
	PermDeploy Permission = "deploy"
	// PermLogin allows the one-time admin login.
	PermLogin Permission = "login"
	// PermBackup allows backup run.
	PermBackup Permission = "backup"
	// PermRestore allows backup restore.
	PermRestore Permission = "restore"
)

// Identity is the signed-in person, as the ID token named them.
type Identity struct {
	// Subject is the ID token's sub claim.
	Subject string
	// Email is the person's email.
	Email string
	// Name is the person's display name.
	Name string
	// Groups are the groups the provider puts the person in.
	Groups []string
}

// Session is the signed-in state the session cookie carries.
type Session struct {
	// Identity is the person.
	Identity Identity
	// Expires is when the session ends.
	Expires time.Time
	// CSRF is the token every POST must carry.
	CSRF string
	// Lang is the language the person picked; empty follows the browser.
	Lang string
}

// Flow is the short-lived state of one sign-in, kept between the redirect
// to the provider and the callback.
type Flow struct {
	// State is the OAuth 2.0 state parameter.
	State string
	// Nonce is the OpenID Connect nonce the ID token must carry.
	Nonce string
	// Verifier is the PKCE code verifier.
	Verifier string
	// Return is the local path to go to after sign-in.
	Return string
}

// Signature names the author of a commit.
type Signature struct {
	// Name and Email identify the author.
	Name, Email string
}

// LogEntry is one commit in the history of a file.
type LogEntry struct {
	// SHA is the commit.
	SHA string
	// Author and Email name who made it.
	Author, Email string
	// When is the author's time.
	When time.Time
	// Subject is the first line of the message.
	Subject string
}

// AuditRecord is one line of the audit log: an action, allowed or refused.
type AuditRecord struct {
	// Time is when the action ended or was refused.
	Time time.Time
	// Actor is who asked for it.
	Actor Identity
	// Action is the action: deploy, promote, login, backup or restore.
	Action string
	// Env and Site are what it acted on.
	Env, Site string
	// Commit is the fleet commit, for promote and deploy.
	Commit string
	// Result is ok, failed, refused or busy.
	Result string
	// Detail says more: why it failed or was refused, what a promotion
	// changed.
	Detail string
	// WPUser is the WordPress user an admin login logged in as.
	WPUser string
	// Allowed says whether the access rules allowed it.
	Allowed bool
}

// Authenticator signs people in with OpenID Connect: the code flow with
// PKCE, state and nonce.
type Authenticator interface {
	// AuthURL is the provider's authorization URL for one sign-in.
	AuthURL(state, nonce, verifier string) string
	// Exchange redeems the code, verifies the ID token and its nonce, and
	// returns the person it names.
	Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error)
}

// Sessions keeps the session and the sign-in flow in encrypted cookies.
type Sessions interface {
	// Get reads the session of a request; false when there is none or it
	// expired.
	Get(r *http.Request) (Session, bool)
	// Set writes the session.
	Set(w http.ResponseWriter, s Session) error
	// Clear removes the session.
	Clear(w http.ResponseWriter)
	// New starts a session for a person, with a fresh CSRF token.
	New(id Identity, lang string) (Session, error)
	// CheckCSRF reports whether the request carries the session's token, in
	// the form field csrf or the header X-CSRF-Token.
	CheckCSRF(r *http.Request, s Session) bool
	// SetFlow writes the state of a sign-in.
	SetFlow(w http.ResponseWriter, f Flow) error
	// TakeFlow reads the state of a sign-in and removes it, so it is used
	// once.
	TakeFlow(w http.ResponseWriter, r *http.Request) (Flow, bool)
}

// Authorizer evaluates the access rules. It denies unless a rule allows.
type Authorizer interface {
	// Allows reports whether one of groups may use perm on site in env.
	Allows(groups []string, env, site string, perm Permission) bool
	// Permissions lists what groups may do on site in env.
	Permissions(groups []string, env, site string) []Permission
}

// Repo is the server's clone of the fleet repository.
type Repo interface {
	// Sync fetches the branch, hard-resets the clone to it and returns the
	// head commit.
	Sync(ctx context.Context) (string, error)
	// Head is the commit the clone is at; empty before the first sync.
	Head() string
	// Commit runs edit in the clone, commits the paths it returns with
	// author as the author and pushes. When the push is refused it syncs,
	// runs edit again and pushes once more.
	Commit(ctx context.Context, msg string, author Signature, edit func(dir string) ([]string, error)) (string, error)
	// FileLog lists the last n commits that changed path, relative to the
	// clone, newest first.
	FileLog(ctx context.Context, path string, n int) ([]LogEntry, error)
}

// Environment is one environment of the fleet, as the UI needs it.
type Environment struct {
	// Name is the environment's name.
	Name string
	// PromoteFrom is the environment it publishes from; empty when none.
	PromoteFrom string
}

// Operations are the operations of internal/ops the server runs, on the
// fleet as the clone holds it now. FleetOps implements it; tests pass a
// fake.
type Operations interface {
	FleetReader
	FleetRunner
}

// FleetReader reads the fleet and its clusters without changing anything.
type FleetReader interface {
	// Environments lists the environments, in the fleet's order.
	Environments() ([]Environment, error)
	// Sites lists the names of the sites of env.
	Sites(env string) ([]string, error)
	// ListSites lists the sites of envs with their image and readiness.
	ListSites(ctx context.Context, envs []string) ([]ops.SiteRow, error)
	// ShowSite reads one site in detail.
	ShowSite(ctx context.Context, env, site string) (ops.SiteDetail, error)
	// Diff compares a site's live release with what the clone renders.
	Diff(ctx context.Context, env, site string) (ops.SiteDiff, error)
	// ListBackups lists a site's backups.
	ListBackups(ctx context.Context, env, site string) ([]ops.BackupRow, error)
	// SiteFile is the site's values file, relative to the clone.
	SiteFile(env, site string) (string, error)
	// PlanPromotion says what publishing site into to changes, without
	// writing.
	PlanPromotion(to, site string) (fleet.PromotionPlan, error)
}

// FleetRunner runs the operations that change a site, its files or its
// database.
type FleetRunner interface {
	// Promote publishes site into to in the fleet at dir and returns the
	// files it changed, relative to dir.
	Promote(ctx context.Context, dir, to, site string) ([]string, error)
	// Deploy deploys site in env, recording commit.
	Deploy(ctx context.Context, env, site string, commit ops.Commit, progress ops.Progress) (ops.DeployResult, error)
	// AdminLogin makes a one-time login link, as the WordPress user whose
	// email is matchEmail when there is one.
	AdminLogin(ctx context.Context, env, site, matchEmail string) (ops.Login, error)
	// RunBackup takes a backup now and follows it.
	RunBackup(ctx context.Context, env, site string, progress ops.Progress) (ops.BackupRun, error)
	// Restore restores the backup stamp, the uploads too when asked.
	Restore(ctx context.Context, env, site, stamp string, uploads bool, progress ops.Progress) error
}

// Locker lets one operation run per site and environment; *ops.Locks
// implements it.
type Locker interface {
	// TryLock takes the site for action, or returns a *ops.BusyError.
	TryLock(env, site, action string) (func(), error)
	// Running names the action holding the site.
	Running(env, site string) (string, bool)
}

// Auditor writes one audit record.
type Auditor func(ctx context.Context, r AuditRecord)

// Deps are what the server is built from. Every field but Logger,
// VerifyWebhook and Now is required.
type Deps struct {
	// Auth signs people in.
	Auth Authenticator
	// Sessions keeps the session cookie.
	Sessions Sessions
	// Access evaluates the access rules.
	Access Authorizer
	// Repo is the fleet clone.
	Repo Repo
	// Ops runs the operations.
	Ops Operations
	// Locks lets one operation run per site.
	Locks Locker
	// Audit writes the audit log.
	Audit Auditor
	// Logger logs what is not audit; nil uses slog.Default.
	Logger *slog.Logger
	// MatchEmail logs in to a site's admin as the WordPress user with the
	// person's email, when one is an administrator.
	MatchEmail bool
	// Committer is the server's bot identity, logged with each promotion.
	Committer Signature
	// VerifyWebhook checks a push webhook's body against its signature
	// header; nil turns /hooks/git off.
	VerifyWebhook func(body []byte, signature string) bool
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// ErrMissingDep is returned by New for a required dependency left nil.
var ErrMissingDep = errors.New("missing dependency")

// validate reports the required dependencies left nil.
func (d Deps) validate() error {
	checks := []struct {
		name    string
		missing bool
	}{
		{"Auth", d.Auth == nil},
		{"Sessions", d.Sessions == nil},
		{"Access", d.Access == nil},
		{"Repo", d.Repo == nil},
		{"Ops", d.Ops == nil},
		{"Locks", d.Locks == nil},
		{"Audit", d.Audit == nil},
	}

	var missing []string

	for _, c := range checks {
		if c.missing {
			missing = append(missing, c.name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrMissingDep, strings.Join(missing, ", "))
	}

	return nil
}
