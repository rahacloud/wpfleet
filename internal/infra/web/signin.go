package web

import (
	"net/http"
	"net/url"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// signInPath is the sign-in page, where a request without a session is
// sent. Its button starts the sign-in at /auth/login.
const signInPath = "/auth/signin"

// signIn shows the sign-in page, or sends a person who is signed in on to
// where they were going.
func (s *Server) signIn(c *echo.Context) error {
	back := session.SafeReturnPath(c.QueryParam("return"))

	if _, ok := s.deps.Sessions.Get(c.Request()); ok {
		return c.Redirect(http.StatusSeeOther, back)
	}

	l := s.lang(c)
	q := url.Values{"return": {back}}

	// A language picked on this page goes through the sign-in into the
	// session.
	if picked, ok := i18n.Parse(c.QueryParam(langParam)); ok {
		q.Set(langParam, picked.Code())
	}

	v := ui.SignInView{LoginURL: "/auth/login?" + q.Encode()}

	return render(c, http.StatusOK, ui.SignInPage(s.page(c, l.T(i18n.SignIn)), v))
}
