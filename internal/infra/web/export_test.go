package web

import (
	"context"

	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
)

// SiteView is the data of the site page, for the tests of what the pages
// get rather than how they render it.
func (s *Server) SiteView(ctx context.Context, sess session.Session, env, site string) (ui.SiteView, error) {
	return s.siteView(ctx, sess, env, site)
}

// FleetView is the data of the home page for the person in sess.
func (s *Server) FleetView(ctx context.Context, sess session.Session) (ui.FleetView, error) {
	envs, err := s.deps.Ops.Environments()
	if err != nil {
		return ui.FleetView{Envs: nil, Rows: nil}, err
	}

	listed, _, err := s.visibleEnvironments(sess, envs)
	if err != nil {
		return ui.FleetView{Envs: nil, Rows: nil}, err
	}

	return s.fleetData(ctx, sess, envs, listed)
}
