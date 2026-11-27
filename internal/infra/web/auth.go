package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// flowTokenBytes is the entropy of the state, the nonce and the PKCE
// verifier: 32 bytes give a 43-character verifier, the shortest RFC 7636
// allows.
const flowTokenBytes = 32

// flowToken is a random URL-safe token for the sign-in flow.
func flowToken() string {
	b := make([]byte, flowTokenBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24

	return base64.RawURLEncoding.EncodeToString(b)
}

// login starts a sign-in: it keeps the state, nonce, PKCE verifier and the
// return path in the flow cookie and sends the browser to the provider.
func (s *Server) login(c *echo.Context) error {
	flow := Flow{
		State:    flowToken(),
		Nonce:    flowToken(),
		Verifier: flowToken(),
		Return:   safeReturn(c.QueryParam("return")),
	}

	err := s.deps.Sessions.SetFlow(c.Response(), flow)
	if err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, s.deps.Auth.AuthURL(flow.State, flow.Nonce, flow.Verifier))
}

// callback ends a sign-in: it checks the state, redeems the code, starts the
// session and goes back where the person was.
func (s *Server) callback(c *echo.Context) error {
	ctx := c.Request().Context()

	if e := c.QueryParam("error"); e != "" {
		s.log.WarnContext(ctx, "the provider refused the sign-in",
			"error", e, "description", c.QueryParam("error_description"))

		return s.signInFailed(c)
	}

	flow, ok := s.deps.Sessions.TakeFlow(c.Response(), c.Request())
	if !ok || subtle.ConstantTimeCompare([]byte(flow.State), []byte(c.QueryParam("state"))) != 1 {
		s.log.WarnContext(ctx, "a sign-in callback without its flow or with another state")

		return s.signInFailed(c)
	}

	id, err := s.deps.Auth.Exchange(ctx, c.QueryParam("code"), flow.Verifier, flow.Nonce)
	if err != nil {
		s.log.WarnContext(ctx, "the sign-in code could not be redeemed", "error", err)

		return s.signInFailed(c)
	}

	sess, err := s.deps.Sessions.New(id, "")
	if err != nil {
		return err
	}

	err = s.deps.Sessions.Set(c.Response(), sess)
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "signed in", "email", id.Email, "groups", id.Groups)

	return c.Redirect(http.StatusSeeOther, safeReturn(flow.Return))
}

// signInFailed says the sign-in failed and offers another.
func (s *Server) signInFailed(c *echo.Context) error {
	return s.message(c, http.StatusUnauthorized, i18n.SignIn, i18n.SignInFailed, true, "/auth/login", i18n.SignIn)
}

// logout ends the session. It is a POST with the CSRF token, so another site
// cannot sign a person out.
func (s *Server) logout(c *echo.Context) error {
	sess, ok := s.deps.Sessions.Get(c.Request())
	if ok && !s.deps.Sessions.CheckCSRF(c.Request(), sess) {
		return s.message(c, http.StatusForbidden, i18n.RefusedTitle, i18n.ExpiredForm, true, "/", i18n.SitesTitle)
	}

	s.deps.Sessions.Clear(c.Response())

	return s.message(c, http.StatusOK, i18n.SignedOutTitle, i18n.SignedOut, false, "/auth/login", i18n.SignIn)
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

	return c.Redirect(http.StatusSeeOther, safeReturn(c.FormValue("return")))
}
