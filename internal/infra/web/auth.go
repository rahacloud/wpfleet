package web

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// login starts a sign-in: it keeps the state, nonce, PKCE verifier and the
// return path in the flow cookie and sends the browser to the provider.
func (s *Server) login(c *echo.Context) error {
	flow, err := session.NewFlow(c.QueryParam("return"))
	if err != nil {
		return err
	}

	err = s.deps.Sessions.SetFlow(c.Response(), flow)
	if err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, s.deps.Auth.AuthURL(flow.State, flow.Nonce, flow.Verifier))
}

// The audit actions of signing in and out; they name no site, so they get
// no Kubernetes Event.
const (
	actionSignIn  = "sign-in"
	actionSignOut = "sign-out"
)

// authRecord is the audit record of a sign-in or sign-out by id, which is
// empty when the provider named nobody.
func (s *Server) authRecord(ctx context.Context, action string, id access.Identity, result, detail string) {
	s.audit(ctx, audit.Record{
		Time: s.now(), Actor: id, Action: action, Env: "", Site: "", Commit: "",
		Result: result, Detail: detail, WPUser: "", Allowed: result != audit.ResultDenied,
	})
}

// callback ends a sign-in: it checks the state, redeems the code, starts the
// session and goes back where the person was. Every outcome is audited: a
// sign-in the provider refused, a callback with a forged or used state, a
// code that does not redeem, and a person no access rule matches are
// denied; that person gets no session, only the page that says they have
// no access.
func (s *Server) callback(c *echo.Context) error {
	ctx := c.Request().Context()

	var nobody access.Identity

	if e := c.QueryParam("error"); e != "" {
		s.authRecord(ctx, actionSignIn, nobody, audit.ResultDenied,
			joinDetail("the provider refused the sign-in", joinDetail(e, c.QueryParam("error_description"))))

		return s.signInFailed(c)
	}

	flow, ok := s.deps.Sessions.TakeFlow(c.Response(), c.Request())
	if !ok || !flow.CheckState(c.QueryParam("state")) {
		s.authRecord(ctx, actionSignIn, nobody, audit.ResultDenied, "a callback without its flow or with another state")

		return s.signInFailed(c)
	}

	id, err := s.deps.Auth.Exchange(ctx, c.QueryParam("code"), flow.Verifier, flow.Nonce)
	if err != nil {
		s.authRecord(ctx, actionSignIn, nobody, audit.ResultDenied,
			joinDetail("the sign-in code could not be redeemed", err.Error()))

		return s.signInFailed(c)
	}

	if !s.deps.Access.Any(id.Groups) {
		s.authRecord(ctx, actionSignIn, id, audit.ResultDenied, "no access rule matches the person's groups")

		return s.message(c, http.StatusForbidden, i18n.NoAccessTitle, i18n.NoAccess, true, "", i18n.SignIn)
	}

	sess, err := s.deps.Sessions.New(id, "")
	if err == nil {
		err = s.deps.Sessions.Set(c.Response(), sess)
	}

	if err != nil {
		s.authRecord(ctx, actionSignIn, id, audit.ResultFailed, joinDetail("starting the session", err.Error()))

		return err
	}

	s.authRecord(ctx, actionSignIn, id, audit.ResultOK, "")

	return c.Redirect(http.StatusSeeOther, session.SafeReturnPath(flow.ReturnTo))
}

// signInFailed says the sign-in failed and offers another.
func (s *Server) signInFailed(c *echo.Context) error {
	return s.message(c, http.StatusUnauthorized, i18n.SignIn, i18n.SignInFailed, true, signInPath, i18n.SignIn)
}

// logout ends the session. It is a POST with the CSRF token, so another site
// cannot sign a person out. A sign-out with a session is audited, refused
// or not; one without a session changes nothing and is not.
func (s *Server) logout(c *echo.Context) error {
	ctx := c.Request().Context()

	sess, ok := s.deps.Sessions.Get(c.Request())
	if ok && !s.deps.Sessions.CheckCSRF(c.Request(), sess) {
		s.authRecord(ctx, actionSignOut, sess.Identity, audit.ResultDenied, "csrf token missing or wrong")

		return s.message(c, http.StatusForbidden, i18n.RefusedTitle, i18n.ExpiredForm, true, "/", i18n.SitesTitle)
	}

	s.deps.Sessions.Clear(c.Response())

	if ok {
		s.authRecord(ctx, actionSignOut, sess.Identity, audit.ResultOK, "")
	}

	return s.message(c, http.StatusOK, i18n.SignedOutTitle, i18n.SignedOut, false, signInPath, i18n.SignIn)
}

// setLang stores the language a person picked in their session.
func (s *Server) setLang(c *echo.Context) error {
	sess := sessionOf(c)
	if !s.deps.Sessions.CheckCSRF(c.Request(), sess) {
		return s.message(c, http.StatusForbidden, i18n.RefusedTitle, i18n.ExpiredForm, true, "/", i18n.SitesTitle)
	}

	l, ok := i18n.Parse(c.FormValue("lang"))
	if !ok {
		return echo.ErrBadRequest
	}

	sess.Lang = l.Code()
	err := s.deps.Sessions.Set(c.Response(), sess)
	if err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, session.SafeReturnPath(c.FormValue("return")))
}
