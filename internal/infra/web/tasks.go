package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// taskKeep is how long a finished task's progress stays to be read.
const taskKeep = time.Hour

// taskIDBytes is the entropy of a task's ID.
const taskIDBytes = 16

// keepAlive is how often an idle event stream sends a comment, so proxies do
// not close it.
const keepAlive = 15 * time.Second

// The kinds of a task's events, which are also the SSE event names the task
// page listens to.
const (
	eventLine = "line"
	eventLog  = "log"
	eventDone = "done"
)

// taskEvent is one step of a task. A line and the result are kept as a
// message key with its arguments, so each viewer reads them in their own
// language; a log chunk is kept as it came.
type taskEvent struct {
	kind string
	key  i18n.Key
	args []string
	text string
	ok   bool
}

// render is the event's HTML in l.
func (e taskEvent) render(l i18n.Lang) templ.Component { //nolint:ireturn // each kind renders another component
	switch e.kind {
	case eventLog:
		return ui.TaskText(e.text)
	case eventDone:
		return ui.TaskResult(e.ok, l.T(e.key, e.args...))
	default:
		return ui.TaskText(l.T(e.key, e.args...) + "\n")
	}
}

// task is one action running in the background, with its progress so far.
type task struct {
	id     string
	env    string
	site   string
	action ui.Action

	mu       sync.Mutex
	events   []taskEvent
	done     bool
	finished time.Time
	changed  chan struct{}
}

// add appends an event and wakes every reader.
func (t *task) add(e taskEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.events = append(t.events, e)
	close(t.changed)
	t.changed = make(chan struct{})
}

// finish appends the result and marks the task done.
func (t *task) finish(e taskEvent, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.events = append(t.events, e)
	t.done = true
	t.finished = at
	close(t.changed)
	t.changed = make(chan struct{})
}

// since returns the events from index i on, whether the task is done, and a
// channel closed at the next event.
func (t *task) since(i int) ([]taskEvent, bool, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.events[min(i, len(t.events)):], t.done, t.changed
}

// line appends a line from the catalogue.
func (t *task) line(key i18n.Key, args ...string) {
	t.add(taskEvent{kind: eventLine, key: key, args: args, text: "", ok: false})
}

// progress turns the operations' events into the task's: the deploy and Job
// lines into catalogue lines, the logs as they are. The operations' own end
// line is left out; the task writes its result itself.
func (t *task) progress() ops.Progress {
	return func(e ops.Event) {
		switch e.Kind {
		case ops.EventDeploying:
			t.line(i18n.Deploying, e.Site, e.Env)
		case ops.EventJobStarted:
			t.line(i18n.JobStarted, e.Job)
		case ops.EventLog:
			t.add(taskEvent{kind: eventLog, key: 0, args: nil, text: e.Text, ok: false})
		case ops.EventDone:
		}
	}
}

// taskHub keeps the running and recently finished tasks.
type taskHub struct {
	mu    sync.Mutex
	tasks map[string]*task
	now   func() time.Time
}

// newTaskHub returns an empty hub.
func newTaskHub(now func() time.Time) *taskHub {
	return &taskHub{mu: sync.Mutex{}, tasks: map[string]*task{}, now: now}
}

// create registers a new task and forgets the ones finished long ago.
func (h *taskHub) create(env, site string, action ui.Action) *task {
	b := make([]byte, taskIDBytes)
	_, _ = rand.Read(b) // crypto/rand.Read never fails since Go 1.24

	t := &task{
		id: hex.EncodeToString(b), env: env, site: site, action: action,
		mu: sync.Mutex{}, events: nil, done: false, finished: time.Time{}, changed: make(chan struct{}),
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	cutoff := h.now().Add(-taskKeep)

	for id, old := range h.tasks {
		old.mu.Lock()
		expired := old.done && old.finished.Before(cutoff)
		old.mu.Unlock()

		if expired {
			delete(h.tasks, id)
		}
	}

	h.tasks[t.id] = t

	return t
}

// get finds a task by ID.
func (h *taskHub) get(id string) (*task, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	t, ok := h.tasks[id]

	return t, ok
}

// outcome is how a task's work ended, for its result line and the audit.
type outcome struct {
	err    error
	key    i18n.Key
	args   []string
	commit string
	detail string
}

// start runs work in the background as a task of the person in sess, under
// the lock already taken, and redirects the browser to its progress page.
// When work ends the lock is released, the result is written to the task
// and the action is audited.
func (s *Server) start(
	c *echo.Context, sess session.Session, env, site string, action ui.Action,
	release func(), work func(context.Context, *task) outcome,
) error {
	t := s.tasks.create(env, site, action)
	s.metrics.running.Inc()

	s.wg.Go(func() {
		defer s.metrics.running.Dec()
		defer release()

		out := work(s.base, t)
		rec := s.record(sess, action, env, site)
		rec.Commit, rec.Detail = out.commit, out.detail

		if out.err != nil {
			rec.Result, rec.Detail = audit.ResultFailed, joinDetail(out.detail, out.err.Error())
			args := []string{out.err.Error()}
			t.finish(taskEvent{kind: eventDone, key: i18n.TaskFailed, args: args, text: "", ok: false}, s.now())
		} else {
			rec.Result = audit.ResultOK
			t.finish(taskEvent{kind: eventDone, key: out.key, args: out.args, text: "", ok: true}, s.now())
		}

		s.audit(s.base, rec)
	})

	return c.Redirect(http.StatusSeeOther, "/tasks/"+t.id)
}

// joinDetail puts two details in one, either may be empty.
func joinDetail(a, b string) string {
	if a == "" {
		return b
	}

	if b == "" {
		return a
	}

	return a + ": " + b
}

// watchable finds a task the person may see: they may view its site.
func (s *Server) watchable(c *echo.Context, id string) (*task, error) {
	t, ok := s.tasks.get(id)
	if !ok {
		return nil, echo.ErrNotFound
	}

	if !s.allows(sessionOf(c), t.env, t.site, access.View) {
		return nil, echo.ErrForbidden
	}

	return t, nil
}

// taskPage renders the progress page of a task.
func (s *Server) taskPage(c *echo.Context) error {
	t, err := s.watchable(c, c.Param("id"))
	if err != nil {
		return err
	}

	l := s.lang(c)
	view := ui.TaskView{Env: t.env, Site: t.site, Action: t.action, EventsURL: "/events?task=" + t.id}
	title := l.T(i18n.TaskTitle, t.action.Label(l), t.site, t.env)

	return render(c, http.StatusOK, ui.TaskPage(s.page(c, title), view))
}

// events streams a task's progress as server-sent events, from its first
// event on, until it is done or the browser goes away.
func (s *Server) events(c *echo.Context) error {
	t, err := s.watchable(c, c.QueryParam("task"))
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	l := s.lang(c)
	w := c.Response()
	rc := http.NewResponseController(w)

	// A server-wide write timeout would cut a long restore's stream.
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set(echo.HeaderContentType, "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(keepAlive)
	defer ticker.Stop()

	sent := 0

	for {
		evs, done, changed := t.since(sent)

		for _, e := range evs {
			err := writeEvent(ctx, w, e.kind, e.render(l))
			if err != nil {
				return nil // the browser went away; there is nobody to tell
			}
		}

		sent += len(evs)
		_ = rc.Flush()

		if done {
			return nil
		}

		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		case <-ticker.C:
			_, err := fmt.Fprint(w, ": keep-alive\n\n")
			if err != nil {
				return nil // the browser went away; there is nobody to tell
			}

			_ = rc.Flush()
		}
	}
}

// writeEvent writes one server-sent event whose data is a rendered
// component, one data line per line of HTML.
func writeEvent(ctx context.Context, w http.ResponseWriter, name string, comp templ.Component) error {
	var buf bytes.Buffer
	err := comp.Render(ctx, &buf)
	if err != nil {
		return err
	}

	var out strings.Builder

	out.WriteString("event: " + name + "\n")

	for line := range strings.Lines(buf.String()) {
		out.WriteString("data: " + strings.TrimSuffix(line, "\n") + "\n")
	}

	out.WriteString("\n")

	_, err = fmt.Fprint(w, out.String())
	if err != nil {
		return fmt.Errorf("writing an event: %w", err)
	}

	return nil
}
