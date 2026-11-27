package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Names the tests use again and again.
const (
	envDev          = "development"
	envStaging      = "staging"
	envProd         = "production"
	siteBlog        = "blog"
	siteShop        = "shop"
	groupPlatform   = "platform"
	tagOld          = "20261120-090000"
	stagingBlogFile = "environments/staging/blog.yaml"
)

// editor is of the shop team, who may view and log in to staging.
func editor() access.Identity {
	return access.Identity{
		Subject: "editor", Email: "editor@example.com", Name: "An Editor", Groups: []string{"shop-team"},
	}
}

// platform is of the platform team, who may do everything.
func platform() access.Identity {
	return access.Identity{
		Subject: groupPlatform, Email: "ops@example.com", Name: "Platform Person", Groups: []string{groupPlatform},
	}
}

// stranger is in no group the rules name.
func stranger() access.Identity {
	return access.Identity{Subject: "stranger", Email: "stranger@example.com", Name: "", Groups: []string{"nobody"}}
}

// helmRelease is a deployed release with a description.
func helmRelease(site, env, description string) *helm.Release {
	return &helm.Release{
		Name: site, Namespace: "wp-" + env, Revision: 3, Status: "deployed", ChartVersion: "0.1.0",
		Updated: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), Description: description,
	}
}

// harness is a server on fakes.
type harness struct {
	t        *testing.T
	server   *web.Server
	auth     *fakeAuth
	sessions *fakeSessions
	repo     *fakeRepo
	ops      *fakeOps
	locks    *ops.Locks
	audit    *auditLog
}

// newHarness builds a server whose access rules give the editor view and
// login on staging and the platform team everything everywhere.
func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{
		t:        t,
		server:   nil,
		auth:     &fakeAuth{identity: editor()}, //nolint:exhaustruct_v5 // the fake records the rest
		sessions: newFakeSessions(),
		//nolint:exhaustruct_v5 // the fake records the rest
		repo:  &fakeRepo{head: "0123456789abcdef0123456789abcdef01234567", dir: t.TempDir()},
		ops:   newFakeOps(),
		locks: &ops.Locks{},
		audit: &auditLog{}, //nolint:exhaustruct_v5 // zero is empty
	}

	everything := []access.Permission{access.View, access.Deploy, access.Login, access.Backup, access.Restore}
	rules := fakeAccess{
		"shop-team":   {envStaging: {access.View, access.Login}},
		groupPlatform: {envDev: everything, envStaging: everything, envProd: everything},
	}

	server, err := web.New(web.Deps{
		Auth: h.auth, Sessions: h.sessions, Access: rules, Repo: h.repo, Ops: h.ops, Locks: h.locks,
		Audit: h.audit.write, Logger: slog.New(slog.DiscardHandler), MatchEmail: true,
		Committer: gitrepo.Signature{Name: "wpfleet", Email: "wpfleet@example.com"},
		VerifyWebhook: func(body []byte, signature string) bool {
			return signature == "sha256=good" && string(body) == `{"ref":"refs/heads/main"}`
		},
		Now: nil,
	})
	testutil.NoError(t, err)

	h.server = server

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		testutil.NoError(t, server.Shutdown(ctx))
	})

	return h
}

// request serves one request as the person behind cookie, nil for nobody.
func (h *harness) request(
	method, target string, form url.Values, cookie *http.Cookie, header ...string,
) *httptest.ResponseRecorder {
	h.t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	r := httptest.NewRequestWithContext(context.Background(), method, target, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}

	if cookie != nil {
		r.AddCookie(cookie)
	}

	w := httptest.NewRecorder()
	h.server.ServeHTTP(w, r)

	return w
}

// events reads the whole event stream of the task a POST redirected to.
func (h *harness) events(location string, cookie *http.Cookie) *httptest.ResponseRecorder {
	h.t.Helper()

	return h.request(http.MethodGet, "/events?task="+strings.TrimPrefix(location, "/tasks/"), nil, cookie)
}

// as signs a person in and returns their cookie and CSRF token.
func (h *harness) as(id access.Identity, lang string) (*http.Cookie, string) {
	return h.sessions.signIn(id, lang), "csrf-" + id.Subject
}

// wait waits for the background actions.
func (h *harness) wait() {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testutil.NoError(h.t, h.server.Shutdown(ctx))
}

// form is a form with the CSRF token and the given pairs.
func form(csrf string, pairs ...string) url.Values {
	v := url.Values{"csrf": {csrf}}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Set(pairs[i], pairs[i+1])
	}

	return v
}

func TestUnauthenticatedPageRedirectsToLogin(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	w := h.request(http.MethodGet, "/sites/staging/blog?tab=1", nil, nil)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)
	want := "/auth/login?return=" + url.QueryEscape("/sites/staging/blog?tab=1")
	testutil.Equal(t, "location", w.Header().Get("Location"), want)

	w = h.request(http.MethodGet, "/sites/staging/blog/status", nil, nil, "HX-Request", "true")
	testutil.Equal(t, "htmx status", w.Code, http.StatusUnauthorized)
	testutil.Equal(t, "htmx redirect", w.Header().Get("Hx-Redirect"), "/auth/login")

	w = h.request(http.MethodPost, "/sites/staging/blog/deploy", form("x"), nil)
	testutil.Equal(t, "post status", w.Code, http.StatusUnauthorized)
	testutil.Equal(t, "deploys", len(h.ops.called("Deploy")), 0)
}

func TestLoginFlow(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	w := h.request(http.MethodGet, "/auth/login?return=/sites/staging/blog", nil, nil)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)

	location, err := url.Parse(w.Header().Get("Location"))
	testutil.NoError(t, err)
	testutil.Equal(t, "provider", location.Host, "sso.example.com")

	flow := w.Result().Cookies()[0]

	// A callback with another state is refused, and the flow is used up.
	w = h.request(http.MethodGet, "/auth/callback?code=abc&state=forged", nil, flow)
	testutil.Equal(t, "forged state", w.Code, http.StatusUnauthorized)

	w = h.request(http.MethodGet, "/auth/login?return=//evil.example.com/", nil, nil)
	location, err = url.Parse(w.Header().Get("Location"))
	testutil.NoError(t, err)

	state := location.Query().Get("state")
	flow = w.Result().Cookies()[0]

	w = h.request(http.MethodGet, "/auth/callback?code=abc&state="+url.QueryEscape(state), nil, flow)
	testutil.Equal(t, "callback", w.Code, http.StatusSeeOther)
	testutil.Equal(t, "an open redirect is cut to /", w.Header().Get("Location"), "/")
	testutil.Equal(t, "code", h.auth.code, "abc")
	testutil.Equal(t, "nonce passed on", h.auth.nonce, location.Query().Get("nonce"))
	testutil.Equal(t, "verifier passed on", h.auth.verifier, location.Query().Get("challenge-of"))

	cookie := w.Result().Cookies()[0]
	w = h.request(http.MethodGet, "/sites/staging/blog", nil, cookie)
	testutil.Equal(t, "signed in", w.Code, http.StatusOK)
}

func TestFailedExchangeIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.auth.err = errFake

	w := h.request(http.MethodGet, "/auth/login", nil, nil)
	location, err := url.Parse(w.Header().Get("Location"))
	testutil.NoError(t, err)

	callback := "/auth/callback?code=abc&state=" + location.Query().Get("state")
	w = h.request(http.MethodGet, callback, nil, w.Result().Cookies()[0])
	testutil.Equal(t, "status", w.Code, http.StatusUnauthorized)
	testutil.Equal(t, "no session", len(w.Result().Cookies()), 0)
}

func TestCSRFIsRejectedAndAudited(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, _ := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/deploy", form("wrong-token"), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusForbidden)
	testutil.Equal(t, "deploys", len(h.ops.called("Deploy")), 0)

	rec := h.audit.only(t)
	testutil.Equal(t, "result", rec.Result, audit.ResultDenied)
	testutil.Equal(t, "allowed", rec.Allowed, false)
	testutil.Equal(t, "actor", rec.Actor.Email, platform().Email)
	testutil.Contains(t, "detail", rec.Detail, "csrf")

	w = h.request(http.MethodPost, "/lang", url.Values{"lang": {"fa"}}, cookie)
	testutil.Equal(t, "language switch without a token", w.Code, http.StatusForbidden)
}

func TestButtonsFollowPermissions(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	cookie, _ := h.as(editor(), "")
	page := h.request(http.MethodGet, "/sites/staging/blog", nil, cookie).Body.String()

	testutil.Contains(t, "editor page", page, `data-action="login"`, "Open admin", "blog.staging.example.com")

	hidden := []string{
		`data-action="deploy"`, `data-action="promote"`, `data-action="backup"`, "Publish ", "Release", "/diff", "/history",
	}
	for _, hidden := range hidden {
		testutil.NotContains(t, "editor page", page, hidden)
	}

	cookie, _ = h.as(platform(), "")
	page = h.request(http.MethodGet, "/sites/staging/blog", nil, cookie).Body.String()

	testutil.Contains(t, "platform page", page,
		`data-action="login"`, `data-action="deploy"`, `data-action="backup"`, `data-action="promote"`,
		"Publish development to staging", "Release", "01234567", "/sites/staging/blog/diff")
}

func TestRestoreFormNeedsRestore(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	stamp, err := backup.ParseStamp("20261126T020000Z")
	testutil.NoError(t, err)

	h.ops.backups = []ops.BackupRow{{
		Stamp: stamp, DumpSize: 2048, HasDump: true, ChangedFiles: 3,
		Expires: time.Date(2026, 12, 10, 2, 0, 0, 0, time.UTC),
	}}

	cookie, _ := h.as(editor(), "")
	fragment := h.request(http.MethodGet, "/sites/staging/blog/backups", nil, cookie).Body.String()
	testutil.Contains(t, "editor backups", fragment, "2026-11-26 02:00 UTC", "2.0 KiB")
	testutil.NotContains(t, "editor backups", fragment, `data-action="restore"`)

	cookie, _ = h.as(platform(), "")
	fragment = h.request(http.MethodGet, "/sites/staging/blog/backups", nil, cookie).Body.String()
	testutil.Contains(t, "platform backups", fragment,
		`data-action="restore"`, `name="stamp" value="20261126T020000Z"`, `pattern="blog"`)
}

func TestRefusalIsAudited(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(editor(), "")

	for _, action := range []string{"deploy", "promote", "backup", "restore"} {
		f := form(csrf, "confirm", siteBlog, "stamp", "latest")
		w := h.request(http.MethodPost, "/sites/staging/blog/"+action, f, cookie)
		testutil.Equal(t, action+" status", w.Code, http.StatusForbidden)
		testutil.Contains(t, action+" page", w.Body.String(), "You are not allowed to do this.")
	}

	records := h.audit.all()
	testutil.Equal(t, "records", len(records), 4)

	for _, rec := range records {
		testutil.Equal(t, rec.Action+" result", rec.Result, audit.ResultDenied)
		testutil.Equal(t, rec.Action+" allowed", rec.Allowed, false)
		testutil.Equal(t, rec.Action+" env", rec.Env, "staging")
		testutil.Equal(t, rec.Action+" site", rec.Site, "blog")
	}

	testutil.Equal(t, "operations run", len(h.ops.calls), 0)
}

func TestAdminRedirectsWithoutRenderingTheLink(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(editor(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/admin", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)
	testutil.Equal(t, "location", w.Header().Get("Location"), h.ops.login.URL)
	testutil.Equal(t, "cache", w.Header().Get("Cache-Control"), "no-store")
	testutil.Equal(t, "body", w.Body.String(), "")

	calls := h.ops.called("AdminLogin")
	testutil.Equal(t, "logins", len(calls), 1)
	testutil.DeepEqual(t, "matched email", calls[0].args, []string{editor().Email})

	rec := h.audit.only(t)
	testutil.Equal(t, "result", rec.Result, "ok")
	testutil.Equal(t, "wp user", rec.WPUser, "7")
	testutil.NotContains(t, "audit detail", rec.Detail, "SECRET-TOKEN")

	// A failure says so without any link.
	h.ops.loginErr = errFake
	w = h.request(http.MethodPost, "/sites/staging/blog/admin", form(csrf), cookie)
	testutil.Equal(t, "failure status", w.Code, http.StatusBadGateway)
	testutil.NotContains(t, "failure page", w.Body.String(), "SECRET-TOKEN")
}

func TestPersianIsRightToLeft(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	cookie, _ := h.as(editor(), "")
	page := h.request(http.MethodGet, "/sites/staging/blog", nil, cookie, "Accept-Language", "fa-IR,fa;q=0.9").
		Body.String()
	testutil.Contains(t, "browser in Persian", page, `lang="fa"`, `dir="rtl"`, "ورود به پیشخوان", "۲۰۲۶-۱۱-۲۶ ۰۲:۰۰ UTC")

	cookie, _ = h.as(editor(), "fa")
	page = h.request(http.MethodGet, "/sites/staging/blog", nil, cookie, "Accept-Language", "en-US").Body.String()
	testutil.Contains(t, "chose Persian", page, `dir="rtl"`)

	cookie, _ = h.as(editor(), "")
	page = h.request(http.MethodGet, "/sites/staging/blog", nil, cookie, "Accept-Language", "en-US").Body.String()
	testutil.Contains(t, "English", page, `lang="en"`, `dir="ltr"`, "Open admin")
}

func TestLanguageSwitchIsStored(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(editor(), "")

	w := h.request(http.MethodPost, "/lang", form(csrf, "lang", "fa", "return", "/sites/staging/blog"), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)
	testutil.Equal(t, "back", w.Header().Get("Location"), "/sites/staging/blog")

	page := h.request(http.MethodGet, "/sites/staging/blog", nil, w.Result().Cookies()[0]).Body.String()
	testutil.Contains(t, "page", page, `dir="rtl"`)
}

func TestFleetGrid(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	cookie, _ := h.as(platform(), "")
	page := h.request(http.MethodGet, "/", nil, cookie).Body.String()
	testutil.Contains(t, "platform grid", page,
		"development", "staging", "production", "20261127-120000", "Another version in development",
		"/sites/production/blog/status", "Not here")

	cookie, _ = h.as(editor(), "")
	page = h.request(http.MethodGet, "/", nil, cookie).Body.String()
	testutil.Contains(t, "editor grid", page, "/sites/staging/blog", "/sites/staging/shop")
	testutil.NotContains(t, "editor grid", page, "/sites/production/blog")
	testutil.NotContains(t, "editor grid", page, "Another version in development")

	calls := h.ops.called("ListSites")
	testutil.DeepEqual(t, "listed only staging for the editor", calls[len(calls)-1].args, []string{"staging"})
}

func TestNoAccess(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, _ := h.as(stranger(), "")

	w := h.request(http.MethodGet, "/", nil, cookie)
	testutil.Equal(t, "status", w.Code, http.StatusForbidden)
	testutil.Contains(t, "page", w.Body.String(), "Your account has no access to any site.")
	testutil.NotContains(t, "page", w.Body.String(), "/sites/")

	w = h.request(http.MethodGet, "/sites/staging/blog", nil, cookie)
	testutil.Equal(t, "site", w.Code, http.StatusForbidden)

	w = h.request(http.MethodGet, "/sites/staging/nonexistent", nil, cookie)
	testutil.Equal(t, "a refusal before existence", w.Code, http.StatusForbidden)
}

func TestStatusFragment(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.diff.Changed = true

	cookie, _ := h.as(platform(), "")
	fragment := h.request(http.MethodGet, "/sites/staging/blog/status", nil, cookie).Body.String()
	testutil.Contains(t, "platform", fragment, "01234567", "Differs from Git", "2026-11-26 02:00 UTC")

	cookie, _ = h.as(editor(), "")
	fragment = h.request(http.MethodGet, "/sites/staging/blog/status", nil, cookie).Body.String()
	testutil.Contains(t, "editor", fragment, "Last backup")
	testutil.NotContains(t, "editor", fragment, "Commit")
	testutil.NotContains(t, "editor", fragment, "Drift")
}

func TestDeployStreamsProgressAndAudits(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/deploy", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)

	task := w.Header().Get("Location")
	testutil.True(t, "a task page", strings.HasPrefix(task, "/tasks/"))

	page := h.request(http.MethodGet, task, nil, cookie).Body.String()
	testutil.Contains(t, "task page", page, `sse-connect="/events?task=`, "Deploy: blog in staging")

	events := h.events(task, cookie)
	testutil.Equal(t, "content type", events.Header().Get("Content-Type"), "text/event-stream")
	testutil.Contains(t, "events", events.Body.String(),
		"event: line\ndata: <span>Reading the latest settings from Git…\n",
		"Deploying blog in staging…", "event: done", "Deployed revision 4 (deployed).")

	h.wait()

	calls := h.ops.called("Deploy")
	testutil.Equal(t, "deploys", len(calls), 1)
	testutil.DeepEqual(t, "commit", calls[0].args, []string{h.repo.head})
	testutil.Equal(t, "synced first", h.repo.syncCount(), 1)

	rec := h.audit.finished(t)
	testutil.Equal(t, "result", rec.Result, "ok")
	testutil.Equal(t, "commit", rec.Commit, h.repo.head)

	_, held := h.locks.Running("staging", "blog")
	testutil.Equal(t, "lock released", held, false)
}

func TestFailedDeployIsAuditedAsFailed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.deployErr = "timed out waiting for the condition"
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/deploy", form(csrf), cookie)
	events := h.events(w.Header().Get("Location"), cookie)
	testutil.Contains(t, "events", events.Body.String(),
		"Failed: the deploy failed: timed out waiting for the condition", `class="message bad"`)

	h.wait()

	rec := h.audit.finished(t)
	testutil.Equal(t, "result", rec.Result, "failed")
	testutil.Contains(t, "detail", rec.Detail, "timed out")
}

func TestBusySiteIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	release, err := h.locks.TryLock("staging", "blog", "restore")
	testutil.NoError(t, err)

	defer release()

	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/backup", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusConflict)
	testutil.Contains(t, "page", w.Body.String(), "Restore is already running on blog in staging.")
	testutil.Equal(t, "backups", len(h.ops.called("RunBackup")), 0)

	rec := h.audit.only(t)
	testutil.Equal(t, "result", rec.Result, "busy")
	testutil.Equal(t, "allowed", rec.Allowed, true)

	page := h.request(http.MethodGet, "/sites/staging/blog", nil, cookie).Body.String()
	testutil.Contains(t, "site page", page, "Running now: Restore")
}

func TestPromoteCommitsAsThePersonAndDeploys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/promote", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)

	events := h.events(w.Header().Get("Location"), cookie)
	testutil.Contains(t, "events", events.Body.String(), "Published as commit c0ffee10.", "Deployed revision 4")

	h.wait()

	testutil.Equal(t, "commits", len(h.repo.commits), 1)

	commit := h.repo.commits[0]
	testutil.DeepEqual(t, "author", commit.author, gitrepo.Signature{Name: platform().Name, Email: platform().Email})
	testutil.DeepEqual(t, "files", commit.files, []string{stagingBlogFile})
	testutil.Contains(t, "message", commit.msg,
		"Publish blog from development to staging", tagOld+" -> registry.example.com/sites/blog:20261127-120000")

	promotes := h.ops.called("Promote")
	testutil.Equal(t, "promoted in the clone", promotes[0].args[0], h.repo.dir)

	deploys := h.ops.called("Deploy")
	testutil.DeepEqual(t, "deployed the new commit", deploys[0].args, []string{h.repo.head})

	rec := h.audit.finished(t)
	testutil.Equal(t, "action", rec.Action, "promote")
	testutil.Equal(t, "commit", rec.Commit, h.repo.head)
}

func TestPromoteWithoutChangeOnlyDeploys(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.plan.Current = h.ops.plan.Target
	cookie, csrf := h.as(platform(), "")

	h.request(http.MethodPost, "/sites/staging/blog/promote", form(csrf), cookie)
	h.wait()

	testutil.Equal(t, "commits", len(h.repo.commits), 0)
	testutil.Equal(t, "deploys", len(h.ops.called("Deploy")), 1)
}

func TestPromoteFromNowhereIsRefused(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/development/blog/promote", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusBadRequest)
	testutil.Equal(t, "plans", len(h.ops.called("PlanPromotion")), 0)

	rec := h.audit.only(t)
	testutil.Equal(t, "result", rec.Result, audit.ResultDenied)
	testutil.Equal(t, "allowed", rec.Allowed, false)
	testutil.Equal(t, "detail", rec.Detail, "the environment has no promoteFrom")
}

// An action that cannot even read the fleet is still in the audit log.
func TestFleetReadFailureIsAudited(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.envErr = errFake
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/deploy", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusInternalServerError)

	rec := h.audit.only(t)
	testutil.Equal(t, "result", rec.Result, audit.ResultFailed)
	testutil.Contains(t, "detail", rec.Detail, "reading the fleet", errFake.Error())
}

// A login waits for no one: while another action holds the site it is
// refused as busy, so its token never lands in a database being replaced.
func TestAdminIsRefusedWhileBusy(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	release, err := h.locks.TryLock(envStaging, siteBlog, "restore")
	testutil.NoError(t, err)

	defer release()

	cookie, csrf := h.as(editor(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/admin", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusConflict)
	testutil.Equal(t, "logins", len(h.ops.called("AdminLogin")), 0)
	testutil.Equal(t, "result", h.audit.only(t).Result, audit.ResultBusy)
}

func TestRestoreNeedsTheSiteName(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	f := form(csrf, "stamp", "20261126T020000Z", "confirm", siteShop)
	w := h.request(http.MethodPost, "/sites/staging/blog/restore", f, cookie)
	testutil.Equal(t, "status", w.Code, http.StatusBadRequest)
	testutil.Contains(t, "page", w.Body.String(), "nothing was restored")
	testutil.Equal(t, "restores", len(h.ops.called("Restore")), 0)
	refusal := h.audit.only(t)
	testutil.Equal(t, "refusal audited", refusal.Result, audit.ResultDenied)
	testutil.Equal(t, "refusal not allowed", refusal.Allowed, false)

	w = h.request(http.MethodPost, "/sites/staging/blog/restore",
		form(csrf, "stamp", "20261126T020000Z", "confirm", "blog", "uploads", "1"), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)
	h.wait()

	calls := h.ops.called("Restore")
	testutil.DeepEqual(t, "restore", calls[0].args, []string{"20261126T020000Z", "true"})
}

// An action cut off by a stop past the shutdown timeout is canceled, and
// Shutdown waits for it to audit its failure before the process exits.
func TestShutdownAuditsACanceledAction(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.block = true
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/restore",
		form(csrf, "stamp", "20261126T020000Z", "confirm", siteBlog), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusSeeOther)

	records := h.audit.all()
	testutil.Equal(t, "audited when accepted", len(records), 1)
	testutil.Equal(t, "started", records[0].Result, audit.ResultStarted)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	testutil.Error(t, h.server.Shutdown(ctx), "the timeout")

	rec := h.audit.finished(t)
	testutil.Equal(t, "result", rec.Result, audit.ResultFailed)
	testutil.Contains(t, "detail", rec.Detail, context.Canceled.Error())
}

func TestBackupStreamsLogsEscaped(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/backup", form(csrf), cookie)
	events := h.events(w.Header().Get("Location"), cookie)
	testutil.Contains(t, "events", events.Body.String(),
		"Started the job blog-backup-manual.",
		"event: log\ndata: <span>dumping &lt;database&gt;\ndata: </span>",
		"The backup is taken.")
}

// SSE ends a line at a bare CR too, so a CR in a Job's log must not reach
// the stream inside a data line, where it would start a field of its own.
func TestBackupLogCannotEndTheStream(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.ops.backupLog = "50%\r100%\revent: done\rdata:Restored\r\n"
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/staging/blog/backup", form(csrf), cookie)
	body := h.events(w.Header().Get("Location"), cookie).Body.String()

	testutil.NotContains(t, "events", body, "\r")
	testutil.Contains(t, "events", body, "data: <span>50%\ndata: 100%\ndata: event: done\ndata: data:Restored\n")
	testutil.Equal(t, "one done event", strings.Count(body, "\nevent: done\n"), 1)
}

func TestTaskIsHiddenFromOthers(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	w := h.request(http.MethodPost, "/sites/production/blog/backup", form(csrf), cookie)
	task := w.Header().Get("Location")

	other, _ := h.as(editor(), "")
	testutil.Equal(t, "task page", h.request(http.MethodGet, task, nil, other).Code, http.StatusForbidden)
	testutil.Equal(t, "events", h.events(task, other).Code, http.StatusForbidden)
	testutil.Equal(t, "unknown task", h.request(http.MethodGet, "/tasks/nope", nil, cookie).Code, http.StatusNotFound)
}

func TestUnknownSiteIsNotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(platform(), "")

	testutil.Equal(t, "page", h.request(http.MethodGet, "/sites/production/shop", nil, cookie).Code, http.StatusNotFound)

	w := h.request(http.MethodPost, "/sites/production/shop/deploy", form(csrf), cookie)
	testutil.Equal(t, "action", w.Code, http.StatusNotFound)
	testutil.Equal(t, "audited", h.audit.only(t).Detail, "unknown site")
}

func TestWebhook(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	body := `{"ref":"refs/heads/main"}`

	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/git", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", "sha256=bad")

	w := httptest.NewRecorder()
	h.server.ServeHTTP(w, r)
	testutil.Equal(t, "bad signature", w.Code, http.StatusUnauthorized)

	r = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/git", strings.NewReader(body))
	r.Header.Set("X-Gitea-Signature", "sha256=good")

	w = httptest.NewRecorder()
	h.server.ServeHTTP(w, r)
	testutil.Equal(t, "good signature", w.Code, http.StatusAccepted)

	h.wait()
	testutil.Equal(t, "synced", h.repo.syncCount(), 1)
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	testutil.Equal(t, "healthz", h.request(http.MethodGet, "/healthz", nil, nil).Code, http.StatusOK)
	testutil.Equal(t, "readyz", h.request(http.MethodGet, "/readyz", nil, nil).Code, http.StatusOK)

	h.repo.head = ""
	w := h.request(http.MethodGet, "/readyz", nil, nil)
	testutil.Equal(t, "readyz before a sync", w.Code, http.StatusServiceUnavailable)

	w = h.request(http.MethodGet, "/static/htmx-2.0.11.min.js", nil, nil)
	testutil.Equal(t, "htmx", w.Code, http.StatusOK)
	testutil.Contains(t, "cache", w.Header().Get("Cache-Control"), "max-age")

	testutil.Contains(t, "csp", w.Header().Get("Content-Security-Policy"), "script-src 'self'")
}

func TestMetrics(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, _ := h.as(editor(), "")
	h.request(http.MethodPost, "/sites/staging/blog/deploy", form("bad"), cookie)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	h.server.MetricsHandler().ServeHTTP(w, r)
	testutil.Contains(t, "metrics", w.Body.String(), `wpfleet_actions_total{action="deploy",result="denied"} 1`)
}

func TestLogout(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cookie, csrf := h.as(editor(), "")

	w := h.request(http.MethodPost, "/auth/logout", form("bad"), cookie)
	testutil.Equal(t, "without a token", w.Code, http.StatusForbidden)

	w = h.request(http.MethodPost, "/auth/logout", form(csrf), cookie)
	testutil.Equal(t, "status", w.Code, http.StatusOK)
	testutil.Contains(t, "page", w.Body.String(), "You have signed out.")
}

func TestNewNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	_, err := web.New(web.Deps{}) //nolint:exhaustruct_v5 // the point is the missing fields
	testutil.ErrorIs(t, err, web.ErrMissingDep)
	testutil.Contains(t, "error", err.Error(), "Auth", "Sessions", "Audit")
}
