package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// errDeployFailed is the error of a deploy whose release failed.
var errDeployFailed = errors.New("the deploy failed")

// record is an audit record of an action by the person in sess, allowed
// unless the caller says otherwise.
func (s *Server) record(sess session.Session, action ui.Action, env, site string) audit.Record {
	return audit.Record{
		Time: s.now(), Actor: sess.Identity, Action: string(action), Env: env, Site: site,
		Commit: "", Result: "", Detail: "", WPUser: "", Allowed: true,
	}
}

// auditTarget audits the action t with a result and a detail. A denied
// action is never recorded as allowed, so a search for allowed=false finds
// every refusal.
func (s *Server) auditTarget(ctx context.Context, t target, result, detail string) {
	rec := s.record(t.sess, t.action, t.env, t.site)
	rec.Result, rec.Detail, rec.Allowed = result, detail, result != audit.ResultDenied
	s.audit(ctx, rec)
}

// audit writes a record and counts it.
func (s *Server) audit(ctx context.Context, r audit.Record) {
	r.Time = s.now()
	s.metrics.actions.WithLabelValues(r.Action, r.Result).Inc()
	s.deps.Audit(ctx, r)
}

// target is the site an action runs on, and who asked.
type target struct {
	sess      session.Session
	env, site string
	action    ui.Action
}

// prepare checks an action before it runs: the CSRF token, then every
// permission perms names, then that the site exists. A refusal is audited
// and rendered, and prepare returns false; the caller then returns the
// error, which is nil when the refusal page was written.
func (s *Server) prepare(c *echo.Context, action ui.Action, perms ...access.Permission) (target, bool, error) {
	sess := sessionOf(c)
	t := target{sess: sess, env: c.Param("env"), site: c.Param("site"), action: action}
	l := s.lang(c)
	back := ui.SitePath(t.env, t.site)

	refuse := func(status int, detail, text string) (target, bool, error) {
		s.auditTarget(c.Request().Context(), t, audit.ResultDenied, detail)

		return t, false, s.refuse(c, status, text, back)
	}

	if !s.deps.Sessions.CheckCSRF(c.Request(), sess) {
		return refuse(http.StatusForbidden, "csrf token missing or wrong", l.T(i18n.ExpiredForm))
	}

	for _, p := range perms {
		if !s.allows(sess, t.env, t.site, p) {
			return refuse(http.StatusForbidden, "no "+string(p)+" permission", l.T(i18n.Forbidden))
		}
	}

	ok, err := s.siteExists(t.env, t.site)
	if err != nil {
		s.auditTarget(c.Request().Context(), t, audit.ResultFailed, joinDetail("reading the fleet", err.Error()))

		return t, false, err
	}

	if !ok {
		return refuse(http.StatusNotFound, "unknown site", l.T(i18n.UnknownSite))
	}

	return t, true, nil
}

// lock takes the site for the action. When another action holds it, the
// refusal is audited and rendered and lock returns false.
func (s *Server) lock(c *echo.Context, t target) (func(), bool, error) {
	release, err := s.deps.Locks.TryLock(t.env, t.site, string(t.action))
	if err == nil {
		return release, true, nil
	}

	busy, ok := errors.AsType[*ops.BusyError](err)
	if !ok {
		return nil, false, err
	}

	s.auditTarget(c.Request().Context(), t, audit.ResultBusy, busy.Error())

	l := s.lang(c)
	text := l.T(i18n.Busy, ui.Action(busy.Action).Label(l), t.site, t.env)

	return nil, false, s.refuse(c, http.StatusConflict, text, ui.SitePath(t.env, t.site))
}

// deploy deploys what the branch holds now: it syncs the clone first, so the
// release records the commit it was rendered from.
func (s *Server) deploy(c *echo.Context) error {
	t, ok, err := s.prepare(c, ui.ActionDeploy, access.Deploy)
	if !ok {
		return err
	}

	release, ok, err := s.lock(c, t)
	if !ok {
		return err
	}

	return s.start(c, t.sess, t.env, t.site, t.action, release, func(ctx context.Context, tk *task) outcome {
		tk.line(i18n.Syncing)

		sha, err := s.deps.Repo.Sync(ctx)
		if err != nil {
			return outcome{err: fmt.Errorf("syncing the fleet repository: %w", err), key: 0, args: nil, commit: "", detail: ""}
		}

		return s.deployAt(ctx, tk, t, sha, "")
	})
}

// deployAt deploys the site at the commit sha and says how it went.
func (s *Server) deployAt(ctx context.Context, tk *task, t target, sha, detail string) outcome {
	res, err := s.deps.Ops.Deploy(ctx, t.env, t.site, ops.Commit{SHA: sha, Dirty: false}, tk.progress())

	switch {
	case err != nil:
		return outcome{err: err, key: 0, args: nil, commit: sha, detail: detail}
	case res.Error != "":
		return outcome{err: fmt.Errorf("%w: %s", errDeployFailed, res.Error), key: 0, args: nil, commit: sha, detail: detail}
	default:
		args := []string{strconv.Itoa(res.Revision), res.Status}

		return outcome{err: nil, key: i18n.Deployed, args: args, commit: sha, detail: detail}
	}
}

// promote publishes the version of the environment this one promotes from,
// as a commit authored by the person, and deploys it. It needs deploy here
// and view on the source.
func (s *Server) promote(c *echo.Context) error {
	t, ok, err := s.prepare(c, ui.ActionPromote, access.Deploy)
	if !ok {
		return err
	}

	ctx := c.Request().Context()

	env, _, err := s.environment(t.env)
	if err != nil {
		s.auditTarget(ctx, t, audit.ResultFailed, joinDetail("reading the fleet", err.Error()))

		return err
	}

	l := s.lang(c)

	if env.PromoteFrom == "" {
		s.auditTarget(ctx, t, audit.ResultDenied, "the environment has no promoteFrom")

		return s.refuse(c, http.StatusBadRequest, l.T(i18n.NoPromoteFrom), ui.SitePath(t.env, t.site))
	}

	if !s.allows(t.sess, env.PromoteFrom, t.site, access.View) {
		s.auditTarget(ctx, t, audit.ResultDenied, "no view permission on "+env.PromoteFrom)

		return s.refuse(c, http.StatusForbidden, l.T(i18n.Forbidden), ui.SitePath(t.env, t.site))
	}

	release, ok, err := s.lock(c, t)
	if !ok {
		return err
	}

	return s.start(c, t.sess, t.env, t.site, t.action, release, func(ctx context.Context, tk *task) outcome {
		return s.publish(ctx, tk, t)
	})
}

// publish commits the promotion, unless the target already runs the
// version, and deploys the commit.
func (s *Server) publish(ctx context.Context, tk *task, t target) outcome {
	fail := func(err error, detail string) outcome {
		return outcome{err: err, key: 0, args: nil, commit: "", detail: detail}
	}

	plan, err := s.deps.Ops.PlanPromotion(t.env, t.site)
	if err != nil {
		return fail(err, "")
	}

	detail := plan.Current.String() + " -> " + plan.Target.String()

	var sha string

	if plan.Changed() {
		msg := fmt.Sprintf("Publish %s from %s to %s\n\n%s\n\nRequested in wpfleet by %s.\n",
			t.site, plan.From, plan.To, detail, displayName(t.sess.Identity))
		author := gitrepo.Signature{Name: authorName(t.sess.Identity), Email: t.sess.Identity.Email}

		sha, err = s.deps.Repo.Commit(ctx, msg, author, func(dir string) ([]string, error) {
			return s.deps.Ops.Promote(ctx, dir, t.env, t.site)
		})
		if err != nil {
			return fail(fmt.Errorf("committing the promotion: %w", err), detail)
		}

		tk.line(i18n.Published, ui.ShortSHA(sha))
	} else {
		tk.line(i18n.AlreadyPublished)

		sha, err = s.deps.Repo.Sync(ctx)
		if err != nil {
			return fail(fmt.Errorf("syncing the fleet repository: %w", err), detail)
		}
	}

	return s.deployAt(ctx, tk, t, sha, detail)
}

// authorName is the name a person's commits carry.
func authorName(id access.Identity) string {
	if id.Name != "" {
		return id.Name
	}

	return id.Email
}

// admin makes a one-time login link and redirects the browser to it. The
// link is never rendered: it is only in the Location header of a response
// that is not cached. It holds the site's lock while it makes the link, so
// it never writes its token into a database a restore is replacing.
func (s *Server) admin(c *echo.Context) error {
	t, ok, err := s.prepare(c, ui.ActionLogin, access.Login)
	if !ok {
		return err
	}

	release, ok, err := s.lock(c, t)
	if !ok {
		return err
	}

	ctx := c.Request().Context()

	email := ""
	if s.deps.MatchEmail {
		email = t.sess.Identity.Email
	}

	login, err := s.deps.Ops.AdminLogin(ctx, t.env, t.site, email)

	release()

	rec := s.record(t.sess, t.action, t.env, t.site)

	if err != nil {
		rec.Result, rec.Detail = audit.ResultFailed, err.Error()
		s.audit(ctx, rec)

		return s.refuse(c, http.StatusBadGateway, s.lang(c).T(i18n.ServerError), ui.SitePath(t.env, t.site))
	}

	rec.Result = audit.ResultOK
	rec.WPUser = strconv.Itoa(login.UserID)

	if login.MatchedEmail {
		rec.Detail = "matched the person's email"
	}

	s.audit(ctx, rec)
	c.Response().Header().Set("Cache-Control", "no-store")

	return c.Redirect(http.StatusSeeOther, login.URL)
}

// backup takes a backup now from the site's CronJob and follows it.
func (s *Server) backup(c *echo.Context) error {
	t, ok, err := s.prepare(c, ui.ActionBackup, access.Backup)
	if !ok {
		return err
	}

	release, ok, err := s.lock(c, t)
	if !ok {
		return err
	}

	return s.start(c, t.sess, t.env, t.site, t.action, release, func(ctx context.Context, tk *task) outcome {
		run, err := s.deps.Ops.RunBackup(ctx, t.env, t.site, tk.progress())
		if err != nil {
			return outcome{err: err, key: 0, args: nil, commit: "", detail: run.Job}
		}

		return outcome{err: nil, key: i18n.BackedUp, args: nil, commit: "", detail: run.Job}
	})
}

// restore restores a backup once the site's name was typed to confirm it.
func (s *Server) restore(c *echo.Context) error {
	t, ok, err := s.prepare(c, ui.ActionRestore, access.Restore)
	if !ok {
		return err
	}

	stamp := c.FormValue("stamp")
	uploads := c.FormValue("uploads") == "1"

	if c.FormValue("confirm") != t.site || stamp == "" {
		s.auditTarget(c.Request().Context(), t, audit.ResultDenied, "the typed confirmation did not match the site's name")

		return s.refuse(c, http.StatusBadRequest, s.lang(c).T(i18n.RestoreMismatch), ui.SitePath(t.env, t.site))
	}

	release, ok, err := s.lock(c, t)
	if !ok {
		return err
	}

	detail := "stamp " + stamp
	if uploads {
		detail += " with uploads"
	}

	return s.start(c, t.sess, t.env, t.site, t.action, release, func(ctx context.Context, tk *task) outcome {
		err := s.deps.Ops.Restore(ctx, t.env, t.site, stamp, uploads, tk.progress())
		if err != nil {
			return outcome{err: err, key: 0, args: nil, commit: "", detail: detail}
		}

		return outcome{err: nil, key: i18n.Restored, args: nil, commit: "", detail: detail}
	})
}
