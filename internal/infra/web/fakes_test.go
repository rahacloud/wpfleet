package web_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// sessionCookie is the cookie the fake sessions keep a session under.
const sessionCookie = "session"

// flowCookie is the cookie the fake sessions keep the sign-in flow under.
const flowCookie = "flow"

// errFake is the error the fakes fail with.
var errFake = errors.New("fake failure")

// fakeAuth is an Authenticator that names one person.
type fakeAuth struct {
	mu       sync.Mutex
	identity access.Identity
	err      error
	code     string
	verifier string
	nonce    string
}

func (a *fakeAuth) AuthURL(state, nonce, verifier string) string {
	return "https://sso.example.com/auth?state=" + state + "&nonce=" + nonce + "&challenge-of=" + verifier
}

func (a *fakeAuth) Exchange(_ context.Context, code, verifier, nonce string) (access.Identity, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.code, a.verifier, a.nonce = code, verifier, nonce

	return a.identity, a.err
}

// fakeSessions keeps sessions and flows in memory, keyed by a cookie value.
type fakeSessions struct {
	mu       sync.Mutex
	sessions map[string]session.Session
	flows    map[string]session.Flow
	next     int
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		mu: sync.Mutex{}, sessions: map[string]session.Session{}, flows: map[string]session.Flow{}, next: 0,
	}
}

func (f *fakeSessions) Get(r *http.Request) (session.Session, bool) {
	var none session.Session

	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return none, false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[c.Value]

	return s, ok
}

func (f *fakeSessions) Set(w http.ResponseWriter, s session.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := f.id()
	f.sessions[id] = s
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id}) //nolint:exhaustruct_v5,gosec // a test cookie

	return nil
}

func (f *fakeSessions) Clear(w http.ResponseWriter) {
	//nolint:exhaustruct_v5,gosec // a test cookie
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", MaxAge: -1})
}

func (f *fakeSessions) New(id access.Identity, lang string) (session.Session, error) {
	return session.Session{Identity: id, Expires: time.Now().Add(time.Hour), CSRF: "csrf-" + id.Subject, Lang: lang}, nil
}

func (f *fakeSessions) CheckCSRF(r *http.Request, s session.Session) bool {
	got := r.FormValue("csrf")
	if got == "" {
		got = r.Header.Get("X-Csrf-Token")
	}

	return got != "" && got == s.CSRF
}

func (f *fakeSessions) SetFlow(w http.ResponseWriter, flow session.Flow) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := f.id()
	f.flows[id] = flow
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: id}) //nolint:exhaustruct_v5,gosec // a test cookie

	return nil
}

func (f *fakeSessions) TakeFlow(_ http.ResponseWriter, r *http.Request) (session.Flow, bool) {
	var none session.Flow

	c, err := r.Cookie(flowCookie)
	if err != nil {
		return none, false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	flow, ok := f.flows[c.Value]
	delete(f.flows, c.Value)

	return flow, ok
}

func (f *fakeSessions) id() string {
	f.next++

	return strconv.Itoa(f.next)
}

// signIn stores a session for id and returns its cookie.
func (f *fakeSessions) signIn(id access.Identity, lang string) *http.Cookie {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := f.id()
	f.sessions[key] = session.Session{
		Identity: id, Expires: time.Now().Add(time.Hour), CSRF: "csrf-" + id.Subject, Lang: lang,
	}

	return &http.Cookie{Name: sessionCookie, Value: key} //nolint:exhaustruct_v5,gosec // a test cookie
}

// fakeAccess grants each group permissions per environment, on every site;
// every permission implies view, as the real rules do.
type fakeAccess map[string]map[string][]access.Permission

func (a fakeAccess) Permissions(groups []string, env, _ string) []access.Permission {
	var granted []access.Permission

	for _, g := range groups {
		for _, p := range a[g][env] {
			if !slices.Contains(granted, p) {
				granted = append(granted, p)
			}
		}
	}

	if len(granted) > 0 && !slices.Contains(granted, access.View) {
		granted = append(granted, access.View)
	}

	return granted
}

func (a fakeAccess) Allows(groups []string, env, site string, perm access.Permission) bool {
	return slices.Contains(a.Permissions(groups, env, site), perm)
}

// commitCall is one call of fakeRepo.Commit.
type commitCall struct {
	msg    string
	author gitrepo.Signature
	files  []string
}

// fakeRepo is a clone whose head moves on every commit.
type fakeRepo struct {
	mu      sync.Mutex
	onSync  func()
	head    string
	dir     string
	syncs   int
	commits []commitCall
	log     []gitrepo.LogEntry
	logPath string
}

func (r *fakeRepo) Sync(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.syncs++
	if r.onSync != nil {
		r.onSync()
	}

	return r.head, nil
}

func (r *fakeRepo) Head() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.head
}

func (r *fakeRepo) Commit(
	_ context.Context, msg string, author gitrepo.Signature, edit gitrepo.Edit,
) (string, error) {
	files, err := edit(r.dir)
	if err != nil {
		return "", err
	}

	if len(files) == 0 {
		return "", gitrepo.ErrNothingToCommit
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.commits = append(r.commits, commitCall{msg: msg, author: author, files: files})
	r.head = "c0ffee" + strconv.Itoa(len(r.commits)) + "0000000000000000000000000000000000"

	return r.head, nil
}

func (r *fakeRepo) FileLog(_ context.Context, path string, _ int) ([]gitrepo.LogEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logPath = path

	return r.log, nil
}

func (r *fakeRepo) syncCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.syncs
}

// call is one call of a fake operation.
type call struct {
	op, env, site string
	args          []string
}

// fakeOps is a fleet of development, staging and production with blog and
// shop, answering every operation from its fields.
type fakeOps struct {
	mu         sync.Mutex
	calls      []call
	envs       []web.Environment
	sites      map[string][]string
	tags       map[string]string
	ready      string
	release    *fakeRelease
	diff       ops.SiteDiff
	backups    []ops.BackupRow
	backupsOff bool
	listErr    error
	login      ops.Login
	loginErr   error
	deployErr  string
	backupLog  string
	envErr     error
	backupErr  error
	block      bool
	plan       fleet.PromotionPlan
	applied    *fleet.PromotionPlan
}

// fakeRelease is the release ShowSite reports.
type fakeRelease struct {
	description string
}

func newFakeOps() *fakeOps {
	return &fakeOps{
		mu:         sync.Mutex{},
		calls:      nil,
		backups:    nil,
		backupsOff: false,
		listErr:    nil,
		loginErr:   nil,
		deployErr:  "",
		backupLog:  "dumping <database>\n",
		envErr:     nil,
		backupErr:  nil,
		block:      false,
		applied:    nil,
		envs: []web.Environment{
			{Name: envDev, PromoteFrom: ""},
			{Name: envStaging, PromoteFrom: envDev},
			{Name: envProd, PromoteFrom: envStaging},
		},
		sites: map[string][]string{
			envDev:     {siteBlog, siteShop},
			envStaging: {siteBlog, siteShop},
			envProd:    {siteBlog},
		},
		tags: map[string]string{
			envDev:     "20261127-120000",
			envStaging: tagOld,
			envProd:    tagOld,
		},
		ready:   "1/1",
		release: &fakeRelease{description: "wpfleet 0123456789abcdef0123456789abcdef01234567"},
		diff:    ops.SiteDiff{Site: siteBlog, Env: envStaging, Installed: true, Changed: false, Diff: "", Error: ""},
		login: ops.Login{
			URL:          "https://blog.staging.example.com/?wpfleet-login=SECRET-TOKEN-NEVER-RENDERED",
			UserID:       7,
			Expires:      time.Now().Add(time.Minute),
			MatchedEmail: true,
		},
		plan: fleet.PromotionPlan{
			Site: siteBlog, From: envDev, To: envStaging,
			Current: fleet.ImageRef{Repository: "registry.example.com/sites/blog", Tag: tagOld, Digest: ""},
			Target:  fleet.ImageRef{Repository: "registry.example.com/sites/blog", Tag: "20261127-120000", Digest: ""},
		},
	}
}

func (o *fakeOps) Environments() ([]web.Environment, error) { return o.envs, o.envErr }

func (o *fakeOps) Sites(env string) ([]string, error) { return o.sites[env], nil }

func (o *fakeOps) ListSites(_ context.Context, envs []string) ([]ops.SiteRow, error) {
	o.record("ListSites", "", "", envs...)

	var rows []ops.SiteRow

	for _, env := range envs {
		for _, site := range o.sites[env] {
			host := site + "." + env + ".example.com"
			rows = append(rows, ops.SiteRow{Site: site, Env: env, Host: host, Image: o.image(env, site), Ready: o.ready})
		}
	}

	return rows, nil
}

func (o *fakeOps) ShowSite(_ context.Context, env, site string) (ops.SiteDetail, error) {
	o.record("ShowSite", env, site)

	last := time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC)
	d := ops.SiteDetail{
		Site: site, Env: env, Namespace: "wp-" + env, Host: site + "." + env + ".example.com", Image: o.image(env, site),
		Release: nil,
		Pods:    []ops.SitePod{{Name: site + "-wordpress-0", Node: "node-a", Phase: "Running", Ready: true}},
		Volume: ops.SiteVolume{
			Claim: site + "-uploads", Found: true, Phase: "Bound", Capacity: "10Gi", StorageClass: "standard",
		},
		Backup: ops.SiteBackup{Enabled: true, CronJob: site + "-backup", Found: true, LastSuccess: &last},
	}

	if o.release != nil {
		d.Release = helmRelease(site, env, o.release.description)
	}

	return d, nil
}

func (o *fakeOps) Diff(_ context.Context, env, site string) (ops.SiteDiff, error) {
	o.record("Diff", env, site)

	return o.diff, nil
}

func (o *fakeOps) ListBackups(_ context.Context, env, site string) ([]ops.BackupRow, error) {
	o.record("ListBackups", env, site)

	if o.listErr != nil {
		return nil, o.listErr
	}

	return o.backups, nil
}

func (o *fakeOps) BackupsEnabled(_, _ string) (bool, error) {
	return !o.backupsOff, nil
}

func (o *fakeOps) SiteFile(env, site string) (string, error) {
	return "environments/" + env + "/" + site + ".yaml", nil
}

func (o *fakeOps) PlanPromotion(to, site string) (fleet.PromotionPlan, error) {
	o.record("PlanPromotion", to, site)

	return o.plan, nil
}

// Promote applies the plan, or applied when the test moved the branch under
// the promotion.
func (o *fakeOps) Promote(_ context.Context, dir, to, site string) (fleet.PromotionPlan, []string, error) {
	o.record("Promote", to, site, dir)

	plan := o.plan
	if o.applied != nil {
		plan = *o.applied
	}

	if !plan.Changed() {
		return plan, nil, nil
	}

	return plan, []string{"environments/" + to + "/" + site + ".yaml"}, nil
}

func (o *fakeOps) Deploy(
	_ context.Context, env, site string, commit ops.Commit, progress ops.Progress,
) (ops.DeployResult, error) {
	o.record("Deploy", env, site, commit.SHA)
	progress(ops.Event{Kind: ops.EventDeploying, Env: env, Site: site, Job: "", Namespace: "wp-" + env, Text: "Deploying"})

	res := ops.DeployResult{Site: site, Env: env, Revision: 4, Status: "deployed", Updated: time.Now(), Error: ""}
	if o.deployErr != "" {
		res.Status, res.Error = ops.StatusFailed, o.deployErr
	}

	return res, nil
}

func (o *fakeOps) AdminLogin(_ context.Context, env, site, matchEmail string) (ops.Login, error) {
	o.record("AdminLogin", env, site, matchEmail)

	return o.login, o.loginErr
}

func (o *fakeOps) RunBackup(_ context.Context, env, site string, progress ops.Progress) (ops.BackupRun, error) {
	o.record("RunBackup", env, site)
	job := site + "-backup-manual"
	progress(ops.Event{Kind: ops.EventJobStarted, Env: env, Site: site, Job: job, Namespace: "wp-" + env, Text: ""})
	progress(ops.Event{Kind: ops.EventLog, Env: env, Site: site, Job: "", Namespace: "", Text: o.backupLog})

	return ops.BackupRun{Job: job, Namespace: "wp-" + env}, o.backupErr
}

func (o *fakeOps) Restore(ctx context.Context, env, site, stamp string, uploads bool, _ ops.Progress) error {
	o.record("Restore", env, site, stamp, strconv.FormatBool(uploads))

	if o.block {
		<-ctx.Done()
		// A real restore takes a moment to notice and clean up, which is
		// when an exiting process used to lose its audit record.
		time.Sleep(50 * time.Millisecond)

		return fmt.Errorf("restoring: %w", ctx.Err())
	}

	return nil
}

func (o *fakeOps) record(op, env, site string, args ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, call{op: op, env: env, site: site, args: args})
}

// called returns the calls of op.
func (o *fakeOps) called(op string) []call {
	o.mu.Lock()
	defer o.mu.Unlock()

	var out []call

	for _, c := range o.calls {
		if c.op == op {
			out = append(out, c)
		}
	}

	return out
}

func (o *fakeOps) image(env, site string) string {
	return "registry.example.com/sites/" + site + ":" + o.tags[env]
}

// auditLog records the audit records.
type auditLog struct {
	mu      sync.Mutex
	records []audit.Record
}

func (a *auditLog) write(_ context.Context, r audit.Record) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.records = append(a.records, r)
}

// all returns the records so far.
func (a *auditLog) all() []audit.Record {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.records)
}

// finished returns the end record of the one action that ran, after
// checking that its started record came first.
func (a *auditLog) finished(tb testing.TB) audit.Record {
	tb.Helper()

	records := a.all()
	testutil.Equal(tb, "audit records", len(records), 2)
	testutil.Equal(tb, "first record", records[0].Result, audit.ResultStarted)
	testutil.Equal(tb, "same action", records[0].Action, records[1].Action)

	return records[1]
}

// only returns the one record written, stopping the test otherwise.
func (a *auditLog) only(tb testing.TB) audit.Record {
	tb.Helper()

	records := a.all()
	testutil.Equal(tb, "audit records", len(records), 1)

	return records[0]
}
