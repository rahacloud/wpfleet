package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// historyLength is how many commits of a site file the site page shows.
const historyLength = 20

// releaseHistoryLength is how many revisions of a site's release the site
// page shows; Helm keeps ten.
const releaseHistoryLength = 10

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

// sitePage renders one site in one environment.
func (s *Server) sitePage(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	view, err := s.siteView(c.Request().Context(), sessionOf(c), env, site)
	if err != nil {
		return err
	}

	return render(c, http.StatusOK, ui.SitePage(s.page(c, site+" · "+env), view))
}

// siteView builds the page of site in env for the person in sess. Only the
// fleet failing to load is an error; a cluster that cannot be read marks
// the view Unavailable and the rest still shows.
func (s *Server) siteView(ctx context.Context, sess session.Session, env, site string) (ui.SiteView, error) {
	envs, err := s.deps.Ops.Environments()
	if err != nil {
		return ui.SiteView{}, err
	}

	order := liveOrder(envs)

	i := slices.IndexFunc(order, func(e orderedEnv) bool { return e.Name == env })
	if i < 0 {
		return ui.SiteView{}, echo.ErrNotFound
	}

	environment := order[i]

	perms := s.perms(sess, env, site)
	view := ui.SiteView{
		Env: env, Site: site, Live: environment.live, Host: "", URL: "", Health: ui.HealthUnknown, Image: "", Tag: "",
		PromoteFrom: "", FromTag: "", CanPublish: false,
		Perms: perms, Engineering: perms.Deploy, Running: "", AdminBlockedBy: s.adminBlockedBy(env, site),
		Unavailable: false, Ready: "", Pods: nil, Release: nil, Releases: nil,
		Volume: ui.Volume{Claim: "", Found: false, Phase: "", Capacity: "", StorageClass: ""},
		Backup: ui.BackupState{Enabled: false, LastSuccess: nil}, Others: nil, CSRF: sess.CSRF, Now: s.now(),
	}

	if running, ok := s.deps.Locks.Running(env, site); ok {
		view.Running = ui.Action(running)
	}

	view.Others, err = s.otherEnvironments(sess, order, env, site)
	if err != nil {
		return ui.SiteView{}, err
	}

	detail, err := s.deps.Ops.ShowSite(ctx, env, site)
	if err == nil {
		fillSite(&view, detail)
	} else {
		s.log.WarnContext(ctx, "reading a site", "env", env, "site", site, "error", err)
		view.Unavailable = true
	}

	if view.Release != nil {
		if history := s.releaseHistory(ctx, env, site); len(history) > 0 {
			view.Releases = history
		}
	}

	if environment.PromoteFrom != "" && s.allows(sess, environment.PromoteFrom, site, access.View) {
		plan, err := s.deps.Ops.PlanPromotion(env, site)
		if err == nil {
			view.PromoteFrom = environment.PromoteFrom
			view.FromTag = plan.Target.Tag
			view.CanPublish = perms.Deploy && plan.Changed()
		}
	}

	return view, nil
}

// adminBlockedBy is the action that would make "Open admin" on site in env
// refused as busy now, or empty.
func (s *Server) adminBlockedBy(env, site string) ui.Action {
	running, ok := s.deps.Locks.Blocking(env, site, string(ui.ActionLogin))
	if !ok {
		return ""
	}

	return ui.Action(running)
}

// releaseHistory reads the revisions of the site's release; a failure is
// logged and shows as no history, the rest of the page still renders.
func (s *Server) releaseHistory(ctx context.Context, env, site string) []ui.ReleaseEntry {
	revisions, err := s.deps.Ops.History(ctx, env, site, releaseHistoryLength)
	if err != nil {
		s.log.WarnContext(ctx, "reading a release's history", "env", env, "site", site, "error", err)

		return nil
	}

	entries := make([]ui.ReleaseEntry, 0, len(revisions))
	for _, r := range revisions {
		entries = append(entries, ui.ReleaseEntry{
			Revision: r.Revision, Updated: r.Updated, Status: r.Status,
			Commit: ui.ShortSHA(r.Commit.SHA), Dirty: r.Commit.Dirty, Author: r.Author,
		})
	}

	return entries
}

// otherEnvironments are the environments other than env that have the site
// and that the person may see, in the order of the home page.
func (s *Server) otherEnvironments(sess session.Session, order []orderedEnv, env, site string) ([]ui.EnvLink, error) {
	var others []ui.EnvLink

	for _, e := range order {
		if e.Name == env || !s.allows(sess, e.Name, site, access.View) {
			continue
		}

		sites, err := s.deps.Ops.Sites(e.Name)
		if err != nil {
			return nil, err
		}

		if slices.Contains(sites, site) {
			others = append(others, ui.EnvLink{Env: e.Name, Live: e.live})
		}
	}

	return others, nil
}

// fillSite copies what ShowSite read into the site page.
func fillSite(view *ui.SiteView, d ops.SiteDetail) {
	view.Host = d.Host
	view.URL = d.URL
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
	view.Health = ui.HealthOf(view.Ready)

	if d.Release != nil {
		commit, _ := ops.ParseDescription(d.Release.Description)
		view.Release = &ui.Release{
			Revision: d.Release.Revision, Status: d.Release.Status, Updated: d.Release.Updated,
			Commit: ui.ShortSHA(commit.SHA), Dirty: commit.Dirty,
		}
		// The history starts with the installed release, which is all there
		// is to show when the release history cannot be read.
		view.Releases = []ui.ReleaseEntry{{
			Revision: view.Release.Revision, Updated: view.Release.Updated, Status: view.Release.Status,
			Commit: view.Release.Commit, Dirty: view.Release.Dirty, Author: "",
		}}
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

// backupsFragment lists the site's backups in words, for its page.
func (s *Server) backupsFragment(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	return render(c, http.StatusOK, ui.BackupsFragment(s.lang(c), s.backups(c, env, site)))
}

// restorePage lists the backups to restore, for people with restore.
func (s *Server) restorePage(c *echo.Context) error {
	env, site := c.Param("env"), c.Param("site")
	if !s.allows(sessionOf(c), env, site, access.Restore) {
		return echo.ErrForbidden
	}

	err := s.viewable(c, env, site)
	if err != nil {
		return err
	}

	l := s.lang(c)
	view := ui.RestoreView{Env: env, Site: site, CSRF: sessionOf(c).CSRF, Backups: s.backups(c, env, site)}

	return render(c, http.StatusOK, ui.RestorePage(s.page(c, l.T(i18n.RestoreTitle, site)), view))
}

// backups reads a site's backups. A site whose backups are off has nothing
// to list, so a bucket the server cannot read is then no failure.
func (s *Server) backups(c *echo.Context, env, site string) ui.BackupsView {
	ctx := c.Request().Context()
	view := ui.BackupsView{Off: false, Failed: false, Rows: nil, Now: s.now()}

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

	return view
}
