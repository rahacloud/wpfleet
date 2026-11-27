// Package web is wpfleet serve's HTTP side: the echo routes, the sign-in
// flow, the pages and fragments of the UI, the actions as POSTs with CSRF
// tokens, their progress over server-sent events, the Git webhook, health
// and metrics. It depends on small interfaces for sign-in, sessions, access
// rules, the fleet clone and the operations, so its tests run on fakes and
// the serve command adapts the real packages to them.
package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// maxBody is the largest request body the server reads: forms and webhook
// payloads are small.
const maxBody = 1 << 20

// sessionKey is where the session of a signed-in request is kept on the
// echo context.
const sessionKey = "wpfleet.session"

// contentSecurityPolicy lets the pages load only their own scripts, styles
// and streams. form-action allows any https or http origin because "Open
// admin" posts here and is redirected to the site's own address, and
// browsers check that redirect against form-action.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; " +
	"font-src 'self'; connect-src 'self'; form-action 'self' https: http:; frame-ancestors 'none'; base-uri 'none'"

// Server is wpfleet serve's HTTP handler. It is an http.Handler; the metrics
// are on a separate handler so they can listen on another address.
type Server struct {
	deps    Deps
	echo    *echo.Echo
	log     *slog.Logger
	now     func() time.Time
	tasks   *taskHub
	metrics *metrics
	syncer  *syncer
	brand   *brandAssets

	// base is the context the actions run under, canceled when Shutdown
	// gives up waiting for them.
	base   context.Context //nolint:containedctx // the actions outlive their requests and end with the server
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New builds the server from its dependencies.
func New(d Deps) (*Server, error) {
	err := d.validate()
	if err != nil {
		return nil, err
	}

	log := d.Logger
	if log == nil {
		log = slog.Default()
	}

	now := d.Now
	if now == nil {
		now = time.Now
	}

	brandAssets, err := newBrandAssets(d.Branding, log)
	if err != nil {
		return nil, err
	}

	base, cancel := context.WithCancel(context.Background())
	m := newMetrics()

	s := &Server{
		deps:    d,
		echo:    echo.New(),
		log:     log,
		now:     now,
		tasks:   newTaskHub(now),
		metrics: m,
		syncer:  nil,
		brand:   brandAssets,
		base:    base,
		cancel:  cancel,
		wg:      sync.WaitGroup{},
	}
	s.syncer = newSyncer(d.Repo, log, m, &s.wg)
	s.routes()

	return s, nil
}

// ServeHTTP serves the UI, the sign-in flow, the webhook and the health
// checks.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.echo.ServeHTTP(w, r)
}

// MetricsHandler serves the Prometheus metrics.
func (s *Server) MetricsHandler() http.Handler {
	return s.metrics.handler()
}

// cancelGrace is how long Shutdown still waits, once it canceled the running
// actions, for them to see the cancellation and audit their failure.
const cancelGrace = 10 * time.Second

// Shutdown waits for the running actions and Git syncs to end. When ctx ends
// first it cancels them, waits up to cancelGrace more so each writes its
// failed audit record, and returns ctx's error.
func (s *Server) Shutdown(ctx context.Context) error {
	done := make(chan struct{})

	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.cancel()

		return nil
	case <-ctx.Done():
		s.cancel()

		grace := time.NewTimer(cancelGrace)
		defer grace.Stop()

		select {
		case <-done:
		case <-grace.C:
			s.log.Error("actions still running after they were canceled; their end is not audited")
		}

		return fmt.Errorf("waiting for the running actions: %w", ctx.Err())
	}
}

// routes registers every route.
func (s *Server) routes() {
	e := s.echo
	e.Logger = s.log
	e.HTTPErrorHandler = s.handleError

	e.Use(middleware.Recover(), middleware.BodyLimit(maxBody), securityHeaders)

	e.GET("/healthz", s.healthz)
	e.GET("/readyz", s.readyz)
	e.StaticFS("/static", ui.Static(), cacheStatic)
	e.GET(logoPath, s.serveLogo)
	e.GET(faviconPath, s.serveFavicon)
	e.GET("/favicon.ico", s.serveFavicon)
	e.GET(themePath, s.serveTheme)

	e.GET(signInPath, s.signIn)
	e.GET("/auth/login", s.login)
	e.GET("/auth/callback", s.callback)
	e.POST("/auth/logout", s.logout)
	e.POST("/hooks/git", s.hook)

	g := e.Group("", s.requireSession)
	g.GET("/", s.home)
	g.POST("/lang", s.setLang)
	g.GET("/tasks/:id", s.taskPage)
	g.GET("/events", s.events)

	site := g.Group("/sites/:env/:site")
	site.GET("", s.sitePage)
	site.GET("/status", s.backupStatus)
	site.GET("/diff", s.diffFragment)
	site.GET("/history", s.historyFragment)
	site.GET("/backups", s.backupsFragment)
	site.GET("/restore", s.restorePage)
	site.POST("/deploy", s.deploy)
	site.POST("/promote", s.promote)
	site.POST("/admin", s.admin)
	site.POST("/backup", s.backup)
	site.POST("/restore", s.restore)
}

// securityHeaders sets the headers every response carries; dynamic pages
// are never cached.
func securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		h := c.Response().Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")

		return next(c)
	}
}

// cacheStatic lets browsers keep the static files. A request with a version
// in the query, like the stylesheet's, is kept for a year, since a new file
// is a new address; htmx carries its version in its file name. Others are
// kept for a day.
func cacheStatic(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if c.QueryParam("v") != "" {
			c.Response().Header().Set("Cache-Control", cacheVersioned)
		} else {
			c.Response().Header().Set("Cache-Control", "public, max-age=86400")
		}

		return next(c)
	}
}

// requireSession lets a signed-in request through with its session on the
// context. A page request without one is sent to sign in and comes back;
// htmx gets HX-Redirect; anything else gets 401.
func (s *Server) requireSession(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		sess, ok := s.deps.Sessions.Get(c.Request())
		if ok {
			c.Set(sessionKey, sess)

			return next(c)
		}

		r := c.Request()
		target := signInPath + "?return=" + url.QueryEscape(r.URL.RequestURI())

		switch {
		case r.Header.Get("Hx-Request") == "true":
			c.Response().Header().Set("Hx-Redirect", signInPath)

			return c.NoContent(http.StatusUnauthorized)
		case r.Method == http.MethodGet && r.URL.Path != "/events":
			return c.Redirect(http.StatusSeeOther, target)
		default:
			return s.message(c, http.StatusUnauthorized, i18n.SignIn, i18n.SignInFailed, true, signInPath, i18n.SignIn)
		}
	}
}

// sessionOf is the session requireSession put on the context.
func sessionOf(c *echo.Context) session.Session {
	sess, _ := c.Get(sessionKey).(session.Session)

	return sess
}

// langParam is the query parameter that picks the language of a page shown
// without a session, such as the sign-in page.
const langParam = "lang"

// lang is the request's language: the person's choice, else the one the
// page's address asks for, else the browser's.
func (s *Server) lang(c *echo.Context) i18n.Lang {
	if l, ok := i18n.Parse(sessionOf(c).Lang); ok {
		return l
	}

	if l, ok := i18n.Parse(c.QueryParam(langParam)); ok {
		return l
	}

	return i18n.Match(c.Request().Header.Get("Accept-Language"))
}

// page is the layout's view of a request.
func (s *Server) page(c *echo.Context, title string) ui.Page {
	sess := sessionOf(c)
	l := s.lang(c)

	p := ui.Page{
		L:         l,
		Title:     title,
		User:      displayName(sess.Identity),
		UserEmail: sess.Identity.Email,
		CSRF:      sess.CSRF,
		Return:    c.Request().URL.RequestURI(),
		LangLinks: nil,
		Brand:     s.brand.view(l),
		Version:   s.deps.Version,
	}

	if p.User == "" {
		p.LangLinks = langLinks(c.Request().URL, l)
	}

	return p
}

// langLinks are links to the page at u in every language but l.
func langLinks(u *url.URL, l i18n.Lang) []ui.LangLink {
	var links []ui.LangLink

	for _, other := range i18n.Supported() {
		if other == l {
			continue
		}

		q := u.Query()
		q.Set(langParam, other.Code())
		links = append(links, ui.LangLink{L: other, URL: u.EscapedPath() + "?" + q.Encode()})
	}

	return links
}

// displayName is how the header names a person: by name, else by email.
func displayName(id access.Identity) string {
	if id.Name != "" {
		return id.Name
	}

	return id.Email
}

// render writes a component as an HTML response.
func render(c *echo.Context, status int, comp templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	c.Response().WriteHeader(status)

	return comp.Render(c.Request().Context(), c.Response())
}

// message renders a page that says one thing.
func (s *Server) message(
	c *echo.Context, status int, title, text i18n.Key, bad bool, link string, linkText i18n.Key,
) error {
	l := s.lang(c)
	m := ui.Message{
		Title: l.T(title), Text: l.T(text), Hint: "", Bad: bad, Link: link, LinkText: "", Support: s.support(),
	}

	if link != "" {
		m.LinkText = l.T(linkText)
	}

	if text == i18n.Forbidden {
		m.Hint = l.T(i18n.AskForAccess)
	}

	return render(c, status, ui.MessagePage(s.page(c, m.Title), m))
}

// refuse renders a refusal with a text already in the person's language;
// a refusal for want of a permission also says whom to ask.
func (s *Server) refuse(c *echo.Context, status int, text string, back string) error {
	l := s.lang(c)
	m := ui.Message{
		Title: l.T(i18n.RefusedTitle), Text: text, Hint: "", Bad: true, Link: back, LinkText: "", Support: s.support(),
	}

	if status == http.StatusForbidden && text == l.T(i18n.Forbidden) {
		m.Hint = l.T(i18n.AskForAccess)
	}

	if back != "" {
		m.LinkText = l.T(i18n.Back)
	}

	return render(c, status, ui.MessagePage(s.page(c, m.Title), m))
}

// handleError renders the errors handlers return and the router's 404 and
// 405 as a page in the person's language.
func (s *Server) handleError(c *echo.Context, err error) {
	resp, uerr := echo.UnwrapResponse(c.Response())
	if uerr == nil && resp.Committed {
		s.log.WarnContext(c.Request().Context(), "error after the response started", "path", c.Path(), "error", err)

		return
	}

	status := http.StatusInternalServerError
	// errors.AsType needs a type that is an error; HTTPStatusCoder is not.
	var coder echo.HTTPStatusCoder
	if errors.As(err, &coder) {
		status = coder.StatusCode()
	}

	title, text := i18n.ErrorTitle, i18n.ServerError

	switch status {
	case http.StatusNotFound:
		title, text = i18n.NotFoundTitle, i18n.NotFound
	case http.StatusBadRequest, http.StatusMethodNotAllowed, http.StatusRequestEntityTooLarge:
		text = i18n.BadRequest
	case http.StatusForbidden:
		title, text = i18n.RefusedTitle, i18n.Forbidden
	default:
		s.log.ErrorContext(c.Request().Context(), "request failed", "path", c.Request().URL.Path, "error", err)
	}

	rerr := s.message(c, status, title, text, true, "/", i18n.GoHome)
	if rerr != nil {
		s.log.WarnContext(c.Request().Context(), "writing the error page", "error", rerr)
	}
}

// healthz answers while the process serves.
func (s *Server) healthz(c *echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

// readyz answers once the clone has a commit, so the UI never shows an empty
// fleet.
func (s *Server) readyz(c *echo.Context) error {
	if s.deps.Repo.Head() == "" {
		return c.String(http.StatusServiceUnavailable, "the fleet repository is not synced yet")
	}

	return c.String(http.StatusOK, "ok")
}

// support is whom the pages tell people to ask for help: the branding's
// support page and address, the same ones "Need help?" in the header leads
// to.
func (s *Server) support() ui.Support {
	return ui.Support{Email: s.deps.Branding.SupportEmail, URL: s.deps.Branding.SupportURL}
}
