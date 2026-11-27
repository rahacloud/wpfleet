package web_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// sessionOf is a signed-in session of id, as the pages get it.
func sessionOf(id access.Identity) session.Session {
	return session.Session{Identity: id, Expires: time.Now().Add(time.Hour), CSRF: "csrf-" + id.Subject, Lang: ""}
}

// The site page gets the site's URL with the scheme from its values, and
// the release's history with each revision's commit and author.
func TestSiteViewData(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	view, err := h.server.SiteView(t.Context(), sessionOf(editor()), envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.Equal(t, "url keeps the scheme ShowSite gave", view.URL, "http://blog.staging.example.com")
	testutil.DeepEqual(t, "history", view.History, []ui.ReleaseEntry{
		{
			Revision: 3, Updated: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), Status: statusDeployed,
			Commit: "01234567", Dirty: false, Author: editorName,
		},
		{
			Revision: 2, Updated: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), Status: "superseded",
			Commit: "", Dirty: false, Author: "",
		},
	})
	testutil.Equal(t, "the history asked for", h.ops.called("History")[0].args[0], "10")
	testutil.Equal(t, "admin is free", view.AdminBlockedBy, ui.Action(""))

	// A site without a release has no history to read.
	h.ops.release = nil

	view, err = h.server.SiteView(t.Context(), sessionOf(editor()), envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.Equal(t, "no history", len(view.History), 0)
	testutil.Equal(t, "history read once", len(h.ops.called("History")), 1)

	// A history that cannot be read leaves the rest of the page.
	h.ops.release = &fakeRelease{description: "wpfleet 0123456789abcdef0123456789abcdef01234567"}
	h.ops.historyErr = errFake

	view, err = h.server.SiteView(t.Context(), sessionOf(editor()), envStaging, siteBlog)
	testutil.NoError(t, err)
	testutil.Equal(t, "no history on failure", len(view.History), 0)
	testutil.True(t, "the release still shows", view.Release != nil)
}

func TestFleetViewData(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	view, err := h.server.FleetView(t.Context(), sessionOf(editor()))
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "environments", view.Envs, []string{envStaging})

	cell := view.Rows[0].Cells[0]
	testutil.Equal(t, "site", cell.Site, siteBlog)
	testutil.Equal(t, "url", cell.URL, "https://blog.staging.example.com")
	testutil.Equal(t, "admin is free", cell.AdminBlockedBy, ui.Action(""))

	release, err := h.locks.TryLock(envStaging, siteBlog, string(ui.ActionRestore))
	testutil.NoError(t, err)

	defer release()

	view, err = h.server.FleetView(t.Context(), sessionOf(editor()))
	testutil.NoError(t, err)
	testutil.Equal(t, "a restore blocks the admin", view.Rows[0].Cells[0].AdminBlockedBy, ui.ActionRestore)
	testutil.Equal(t, "shop is free", view.Rows[1].Cells[0].AdminBlockedBy, ui.Action(""))
}

// "Open admin" runs beside a deploy, a publish and a backup, which leave
// the database alone, and the page says the admin can open meanwhile.
func TestAdminRunsBesideADeploy(t *testing.T) {
	t.Parallel()

	for _, running := range []ui.Action{ui.ActionDeploy, ui.ActionPromote, ui.ActionBackup, ui.ActionLogin} {
		h := newHarness(t)

		release, err := h.locks.TryLock(envStaging, siteBlog, string(running))
		testutil.NoError(t, err)

		view, err := h.server.SiteView(t.Context(), sessionOf(editor()), envStaging, siteBlog)
		testutil.NoError(t, err)
		testutil.Equal(t, string(running)+": admin is free", view.AdminBlockedBy, ui.Action(""))
		testutil.Equal(t, string(running)+": running", view.Running, running)

		cookie, csrf := h.as(editor(), "")
		w := h.request(http.MethodPost, "/sites/staging/blog/admin", form(csrf), cookie)
		testutil.Equal(t, string(running)+": status", w.Code, http.StatusSeeOther)
		testutil.Equal(t, string(running)+": result", h.audit.only(t).Result, audit.ResultOK)

		got, _ := h.locks.Running(envStaging, siteBlog)
		testutil.Equal(t, string(running)+": still held by it", got, string(running))

		release()
	}
}

// Beside lets only a login run beside other actions, and never beside a
// restore.
func TestBeside(t *testing.T) {
	t.Parallel()

	actions := []ui.Action{ui.ActionDeploy, ui.ActionPromote, ui.ActionLogin, ui.ActionBackup, ui.ActionRestore}
	for _, a := range actions {
		for _, b := range actions {
			want := (a == ui.ActionLogin || b == ui.ActionLogin) && a != ui.ActionRestore && b != ui.ActionRestore
			testutil.Equal(t, string(a)+" beside "+string(b), web.Beside(string(a), string(b)), want)
		}
	}
}
