package web

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// historyLength is how many commits of a site file the site page shows.
const historyLength = 20

// allows reports whether the session's person may use perm on site in env.
func (s *Server) allows(sess session.Session, env, site string, perm access.Permission) bool {
	return s.deps.Access.Allows(sess.Identity.Groups, env, site, perm)
}

// perms are the actions the session's person may start on site in env.
func (s *Server) perms(sess session.Session, env, site string) ui.Perms {
	granted := s.deps.Access.Permissions(sess.Identity.Groups, env, site)

	return ui.Perms{
		Deploy:  slices.Contains(granted, access.Deploy),
		Login:   slices.Contains(granted, access.Login),
		Backup:  slices.Contains(granted, access.Backup),
		Restore: slices.Contains(granted, access.Restore),
	}
}

// environment finds an environment by name.
func (s *Server) environment(name string) (Environment, bool, error) {
	envs, err := s.deps.Ops.Environments()
	if err != nil {
		return Environment{}, false, err
	}

	i := slices.IndexFunc(envs, func(e Environment) bool { return e.Name == name })
	if i < 0 {
		return Environment{Name: "", PromoteFrom: ""}, false, nil
	}

	return envs[i], true, nil
}

// siteExists reports whether env has site.
func (s *Server) siteExists(env, site string) (bool, error) {
	_, ok, err := s.environment(env)
	if err != nil || !ok {
		return false, err
	}

	sites, err := s.deps.Ops.Sites(env)
	if err != nil {
		return false, err
	}

	return slices.Contains(sites, site), nil
}

// viewable checks that the person may see the site and that it exists:
// a refusal before the existence check, so a refusal says nothing about
// which sites exist.
func (s *Server) viewable(c *echo.Context, env, site string) error {
	if !s.allows(sessionOf(c), env, site, access.View) {
		return echo.ErrForbidden
	}

	ok, err := s.siteExists(env, site)
	if err != nil {
		return err
	}

	if !ok {
		return echo.ErrNotFound
	}

	return nil
}

// fleet renders the grid of sites and environments the person may see. A
// person who may see nothing gets a page that says so and nothing else.
func (s *Server) fleet(c *echo.Context) error {
	sess := sessionOf(c)
	l := s.lang(c)

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

		return render(c, http.StatusOK, ui.FleetPage(s.page(c, l.T(i18n.SitesTitle)), ui.FleetView{Envs: nil, Rows: nil}))
	}

	rows, err := s.deps.Ops.ListSites(c.Request().Context(), listed)
	if err != nil {
		return err
	}

	canView := func(env, site string) bool { return s.allows(sess, env, site, access.View) }
	view := fleetView(envs, listed, canView, rows)

	return render(c, http.StatusOK, ui.FleetPage(s.page(c, l.T(i18n.SitesTitle)), view))
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

// cellKey is one site in one environment.
type cellKey struct{ env, site string }

// fleetView lays the rows out as a grid over the listed environments: a
// cell the person may not see stays empty, and a cell whose promoteFrom
// environment runs another tag says so when the person may see that one.
func fleetView(
	envs []Environment, listed []string, canView func(env, site string) bool, rows []ops.SiteRow,
) ui.FleetView {
	byCell := map[cellKey]ops.SiteRow{}

	var names []string

	for _, r := range rows {
		if !canView(r.Env, r.Site) {
			continue
		}

		byCell[cellKey{r.Env, r.Site}] = r

		if !slices.Contains(names, r.Site) {
			names = append(names, r.Site)
		}
	}

	slices.Sort(names)

	from := map[string]string{}
	for _, e := range envs {
		from[e.Name] = e.PromoteFrom
	}

	view := ui.FleetView{Envs: listed, Rows: make([]ui.FleetRow, 0, len(names))}

	for _, name := range names {
		row := ui.FleetRow{Site: name, Cells: make([]ui.FleetCell, 0, len(listed))}

		for _, env := range listed {
			row.Cells = append(row.Cells, fleetCell(byCell, from, env, name, canView(env, name)))
		}

		view.Rows = append(view.Rows, row)
	}

	return view
}

// fleetCell is one cell of the grid.
func fleetCell(byCell map[cellKey]ops.SiteRow, from map[string]string, env, site string, visible bool) ui.FleetCell {
	cell := ui.FleetCell{Env: env, Site: site, Visible: visible, Present: false, Tag: "", Ready: "", NewerIn: ""}
	if !visible {
		return cell
	}

	r, ok := byCell[cellKey{env, site}]
	if !ok {
		return cell
	}

	cell.Present = true
	cell.Tag = imageTag(r.Image)

	if r.Ready != ops.ReadyUnknown {
		cell.Ready = r.Ready
	}

	if source, ok := byCell[cellKey{from[env], site}]; ok && imageTag(source.Image) != cell.Tag {
		cell.NewerIn = from[env]
	}

	return cell
}

// imageTag is the tag of an image reference, empty when it has none.
func imageTag(image string) string {
	ref, err := fleet.ParseImageRef(image)
	if err != nil {
		return ""
	}

	return ref.Tag
}

// statusFragment is what a grid cell loads after the page: the commit and
// drift for people with deploy, and the last backup.
func (s *Server) statusFragment(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	engineering := s.allows(sessionOf(c), env, site, access.Deploy)

	status := ui.CellStatus{
		Engineering: engineering, Commit: "", Dirty: false, Drift: ui.DriftUnknown,
		BackupEnabled: false, LastBackup: nil,
	}

	detail, err := s.deps.Ops.ShowSite(ctx, env, site)
	if err == nil {
		status.BackupEnabled = detail.Backup.Enabled
		status.LastBackup = detail.Backup.LastSuccess

		if detail.Release != nil {
			if commit, ok := ops.ParseDescription(detail.Release.Description); ok {
				status.Commit, status.Dirty = ui.ShortSHA(commit.SHA), commit.Dirty
			}
		}
	} else {
		s.log.WarnContext(ctx, "reading a site", "env", env, "site", site, "error", err)
	}

	if engineering {
		status.Drift = s.drift(c, env, site)
	}

	return render(c, http.StatusOK, ui.CellStatusFragment(s.lang(c), status))
}

// drift compares the site's release with the branch.
func (s *Server) drift(c *echo.Context, env, site string) ui.Drift {
	d, err := s.deps.Ops.Diff(c.Request().Context(), env, site)

	switch {
	case err != nil || d.Error != "":
		return ui.DriftUnknown
	case !d.Installed:
		return ui.DriftNotInstalled
	case d.Changed:
		return ui.DriftChanged
	default:
		return ui.DriftNone
	}
}

// sitePage renders one site in one environment.
func (s *Server) sitePage(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	sess := sessionOf(c)
	ctx := c.Request().Context()

	environment, _, err := s.environment(env)
	if err != nil {
		return err
	}

	perms := s.perms(sess, env, site)
	view := ui.SiteView{
		Env: env, Site: site, Host: "", URL: "", Image: "", Tag: "",
		PromoteFrom: "", FromTag: "", CanPublish: false,
		Perms: perms, Engineering: perms.Deploy, Running: "", Unavailable: false, Ready: "",
		Pods: nil, Release: nil, Volume: ui.Volume{Claim: "", Found: false, Phase: "", Capacity: "", StorageClass: ""},
		Backup: ui.BackupState{Enabled: false, LastSuccess: nil}, CSRF: sess.CSRF,
	}

	if running, ok := s.deps.Locks.Running(env, site); ok {
		view.Running = ui.Action(running)
	}

	detail, err := s.deps.Ops.ShowSite(ctx, env, site)
	if err == nil {
		fillSite(&view, detail)
	} else {
		s.log.WarnContext(ctx, "reading a site", "env", env, "site", site, "error", err)
		view.Unavailable = true
	}

	if environment.PromoteFrom != "" && s.allows(sess, environment.PromoteFrom, site, access.View) {
		plan, err := s.deps.Ops.PlanPromotion(env, site)
		if err == nil {
			view.PromoteFrom = environment.PromoteFrom
			view.FromTag = plan.Target.Tag
			view.CanPublish = perms.Deploy
		}
	}

	return render(c, http.StatusOK, ui.SitePage(s.page(c, site+" · "+env), view))
}

// fillSite copies what ShowSite read into the site page.
func fillSite(view *ui.SiteView, d ops.SiteDetail) {
	view.Host = d.Host
	if d.Host != "" {
		view.URL = "https://" + d.Host
	}

	view.Image = d.Image
	view.Tag = imageTag(d.Image)
	view.Volume = ui.Volume{
		Claim: d.Volume.Claim, Found: d.Volume.Found, Phase: d.Volume.Phase,
		Capacity: d.Volume.Capacity, StorageClass: d.Volume.StorageClass,
	}
	view.Backup = ui.BackupState{Enabled: d.Backup.Enabled, LastSuccess: d.Backup.LastSuccess}

	ready := 0
	for _, p := range d.Pods {
		view.Pods = append(view.Pods, ui.Pod{Name: p.Name, Node: p.Node, Phase: p.Phase, Ready: p.Ready})

		if p.Ready {
			ready++
		}
	}

	view.Ready = strconv.Itoa(ready) + "/" + strconv.Itoa(len(d.Pods))

	if d.Release != nil {
		commit, _ := ops.ParseDescription(d.Release.Description)
		view.Release = &ui.Release{
			Revision: d.Release.Revision, Status: d.Release.Status, Updated: d.Release.Updated,
			Commit: ui.ShortSHA(commit.SHA), Dirty: commit.Dirty,
		}
	}
}

// diffFragment is what a deploy would change, for people with deploy.
func (s *Server) diffFragment(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	if !s.allows(sessionOf(c), env, site, access.Deploy) {
		return echo.ErrForbidden
	}

	d, err := s.deps.Ops.Diff(c.Request().Context(), env, site)
	view := ui.DiffView{Installed: d.Installed, Changed: d.Changed, Diff: d.Diff, Error: d.Error}

	if err != nil {
		view.Error = err.Error()
	}

	return render(c, http.StatusOK, ui.DiffFragment(s.lang(c), view))
}

// historyFragment is the Git history of the site's file, for people with
// deploy.
func (s *Server) historyFragment(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	if !s.allows(sessionOf(c), env, site, access.Deploy) {
		return echo.ErrForbidden
	}

	ctx := c.Request().Context()

	entries, err := s.history(c, env, site)
	if err != nil {
		s.log.WarnContext(ctx, "reading a site file's history", "env", env, "site", site, "error", err)
	}

	return render(c, http.StatusOK, ui.HistoryFragment(s.lang(c), entries, err != nil))
}

// history reads the last commits of the site's file.
func (s *Server) history(c *echo.Context, env, site string) ([]ui.HistoryEntry, error) {
	path, err := s.deps.Ops.SiteFile(env, site)
	if err != nil {
		return nil, err
	}

	log, err := s.deps.Repo.FileLog(c.Request().Context(), path, historyLength)
	if err != nil {
		return nil, err
	}

	entries := make([]ui.HistoryEntry, 0, len(log))
	for _, e := range log {
		entries = append(entries, ui.HistoryEntry{
			SHA: ui.ShortSHA(e.SHA), Author: e.Author, When: e.When, Subject: e.Subject,
		})
	}

	return entries, nil
}

// backupsFragment lists the site's backups.
func (s *Server) backupsFragment(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	sess := sessionOf(c)
	ctx := c.Request().Context()
	view := ui.BackupsView{
		Env: env, Site: site, CSRF: sess.CSRF, CanRestore: s.allows(sess, env, site, access.Restore),
		Off: false, Failed: false, Rows: nil,
	}

	enabled, err := s.deps.Ops.BackupsEnabled(env, site)
	view.Off = err == nil && !enabled

	rows, err := s.deps.Ops.ListBackups(ctx, env, site)

	switch {
	case err != nil && view.Off:
		// Nothing is backed up, so a bucket the server cannot read hides
		// only backups from before they were turned off.
		s.log.DebugContext(ctx, "listing the backups of a site whose backups are off",
			"env", env, "site", site, "error", err)
	case err != nil:
		s.log.WarnContext(ctx, "listing backups", "env", env, "site", site, "error", err)

		view.Failed = true
	}

	for _, r := range rows {
		view.Rows = append(view.Rows, ui.BackupRow{
			Stamp: r.Stamp.String(), Taken: r.Stamp.Time(), HasDump: r.HasDump,
			DumpSize: r.DumpSize, ChangedFiles: r.ChangedFiles, Expires: r.Expires,
		})
	}

	return render(c, http.StatusOK, ui.BackupsFragment(s.lang(c), view))
}
