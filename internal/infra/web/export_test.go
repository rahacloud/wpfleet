package web

import (
	"context"

	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// SiteView is the data of the site page, for the tests of what the pages
// get rather than how they render it.
func (s *Server) SiteView(ctx context.Context, sess session.Session, env, site string) (ui.SiteView, error) {
	return s.siteView(ctx, sess, i18n.English, env, site)
}

// HomeView is the data of the home page for the person in sess.
func (s *Server) HomeView(ctx context.Context, sess session.Session) (ui.HomeView, error) {
	view, _, err := s.homeView(ctx, sess, i18n.English, "")

	return view, err
}
