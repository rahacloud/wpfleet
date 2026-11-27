package web

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// home renders "Your sites": a card for each site the person may see, with
// its environments live first. A person who may see nothing in a fleet that
// has sites gets a page that says so and nothing else; ?q= filters the cards
// by name or address.
func (s *Server) home(c *echo.Context) error {
	sess := sessionOf(c)
	l := s.lang(c)
	title := l.T(i18n.HomeTitle)
	query := strings.TrimSpace(c.QueryParam("q"))
	view := ui.HomeView{Cards: nil, Total: 0, Query: query, CSRF: sess.CSRF, Support: s.deps.Support}

	envs, err := s.deps.Ops.Environments()
	if err != nil {
		return err
	}

	listed, anySite, err := s.visibleEnvironments(sess, envs)
	if err != nil {
		return err
	}

	if len(listed) == 0 {
		if anySite {
			return s.message(c, http.StatusForbidden, i18n.NoAccessTitle, i18n.NoAccess, true, "", i18n.SignIn)
		}

		return render(c, http.StatusOK, ui.HomePage(s.page(c, title), view))
	}

	rows, err := s.deps.Ops.ListSites(c.Request().Context(), listed)
	if err != nil {
		return err
	}

	rows = slices.DeleteFunc(rows, func(r ops.SiteRow) bool { return !s.allows(sess, r.Env, r.Site, access.View) })
	cards := siteCards(liveOrder(envs), rows, func(env, site string) ui.Perms { return s.perms(sess, env, site) })
	view.Total = len(cards)
	view.Cards = filterCards(cards, query)

	return render(c, http.StatusOK, ui.HomePage(s.page(c, title), view))
}

// visibleEnvironments finds the environments with a site the person may
// see, and whether the fleet has any site at all.
func (s *Server) visibleEnvironments(sess session.Session, envs []Environment) ([]string, bool, error) {
	anySite := false

	var listed []string

	for _, env := range envs {
		sites, err := s.deps.Ops.Sites(env.Name)
		if err != nil {
			return nil, false, err
		}

		anySite = anySite || len(sites) > 0

		if slices.ContainsFunc(sites, func(site string) bool { return s.allows(sess, env.Name, site, access.View) }) {
			listed = append(listed, env.Name)
		}
	}

	return listed, anySite, nil
}

// orderedEnv is an environment in the order the UI shows them.
type orderedEnv struct {
	Environment

	// live says this is the environment visitors see.
	live bool
}

// liveOrder sorts the environments live first, then back along the
// promoteFrom chain: the further an environment is from the start of its
// chain, the earlier it comes, and environments as far apart keep the
// fleet's order. The live ones are the furthest; when nothing promotes into
// anything, only a fleet of one environment has a live one, since otherwise
// the UI cannot tell which of them visitors see.
func liveOrder(envs []Environment) []orderedEnv {
	from := map[string]string{}
	for _, e := range envs {
		from[e.Name] = e.PromoteFrom
	}

	depth := func(name string) int {
		d := 0
		// A chain is never longer than the fleet; the bound stops a loop in
		// a fleet whose promoteFrom goes round in a circle.
		for next := from[name]; next != "" && d < len(envs); next = from[next] {
			d++
		}

		return d
	}

	depths := map[string]int{}
	deepest := 0

	for _, e := range envs {
		depths[e.Name] = depth(e.Name)
		deepest = max(deepest, depths[e.Name])
	}

	out := make([]orderedEnv, 0, len(envs))
	for _, e := range envs {
		live := depths[e.Name] == deepest && (deepest > 0 || len(envs) == 1)
		out = append(out, orderedEnv{Environment: e, live: live})
	}

	slices.SortStableFunc(out, func(a, b orderedEnv) int { return cmp.Compare(depths[b.Name], depths[a.Name]) })

	return out
}

// siteCards builds a card for each site in rows, which hold only the
// environments the person may see: the environments in order, "Open admin"
// where the person may log in, and "Publish" for the environment closest to
// live whose promoteFrom runs another version, when the person may deploy
// there and see the source.
func siteCards(order []orderedEnv, rows []ops.SiteRow, perms func(env, site string) ui.Perms) []ui.SiteCard {
	type key struct{ env, site string }

	byKey := map[key]ops.SiteRow{}

	var names []string

	for _, r := range rows {
		byKey[key{r.Env, r.Site}] = r

		if !slices.Contains(names, r.Site) {
			names = append(names, r.Site)
		}
	}

	slices.Sort(names)

	cards := make([]ui.SiteCard, 0, len(names))

	for _, name := range names {
		card := ui.SiteCard{Site: name, Envs: nil, Publish: nil}

		for _, e := range order {
			r, ok := byKey[key{e.Name, name}]
			if !ok {
				continue
			}

			p := perms(e.Name, name)
			card.Envs = append(card.Envs, ui.CardEnv{
				Env: e.Name, Live: e.live, Health: health(r.Ready), Host: r.Host, URL: rowURL(r), CanLogin: p.Login,
			})

			source, ok := byKey[key{e.PromoteFrom, name}]
			if card.Publish == nil && p.Deploy && ok && imageTag(source.Image) != imageTag(r.Image) {
				card.Publish = &ui.PublishOffer{From: e.PromoteFrom, To: e.Name}
			}
		}

		cards = append(cards, card)
	}

	return cards
}

// health reads a readiness from ListSites, which says ops.ReadyUnknown when
// the cluster could not be read.
func health(ready string) ui.Health {
	if ready == ops.ReadyUnknown {
		return ui.HealthUnknown
	}

	return ui.HealthOf(ready)
}

// rowURL is the link to a site in the list.
func rowURL(r ops.SiteRow) string {
	return hostURL(r.Host)
}

// hostURL is the link to a host, empty for no host.
func hostURL(host string) string {
	if host == "" {
		return ""
	}

	return "https://" + host
}

// filterCards keeps the cards whose site name or one of whose addresses
// contains query, ignoring case; an empty query keeps them all.
func filterCards(cards []ui.SiteCard, query string) []ui.SiteCard {
	if query == "" {
		return cards
	}

	q := strings.ToLower(query)

	return slices.DeleteFunc(cards, func(c ui.SiteCard) bool {
		if strings.Contains(strings.ToLower(c.Site), q) {
			return false
		}

		return !slices.ContainsFunc(c.Envs, func(e ui.CardEnv) bool { return strings.Contains(strings.ToLower(e.Host), q) })
	})
}

// imageTag is the tag of an image reference, empty when it has none.
func imageTag(image string) string {
	ref, err := fleet.ParseImageRef(image)
	if err != nil {
		return ""
	}

	return ref.Tag
}

// backupStatus is what a card loads after the page: the site's last backup,
// in words.
func (s *Server) backupStatus(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	status := ui.BackupStatus{Enabled: false, Last: nil, Now: s.now()}

	detail, err := s.deps.Ops.ShowSite(ctx, env, site)
	if err == nil {
		status.Enabled, status.Last = detail.Backup.Enabled, detail.Backup.LastSuccess
	} else {
		s.log.WarnContext(ctx, "reading a site", "env", env, "site", site, "error", err)
	}

	return render(c, http.StatusOK, ui.BackupStatusFragment(s.lang(c), status))
}
