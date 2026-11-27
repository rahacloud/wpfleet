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

// msgArg is an argument of a catalogue message: a name, passed as it is, or
// a number, written in each viewer's digits.
type msgArg struct {
	text  string
	num   int
	isNum bool
}

// names are arguments that are passed as they are.
func names(texts ...string) []msgArg {
	args := make([]msgArg, 0, len(texts))
	for _, t := range texts {
		args = append(args, msgArg{text: t, num: 0, isNum: false})
	}

	return args
}

// number is an argument written in the viewer's digits.
func number(n int) msgArg {
	return msgArg{text: "", num: n, isNum: true}
}

// in writes the arguments for l.
func in(l i18n.Lang, args []msgArg) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a.isNum {
			out = append(out, l.Int(a.num))
		} else {
			out = append(out, a.text)
		}
	}

	return out
}

// message is a catalogue message with its arguments, kept so each viewer
// reads it in their own language.
type message struct {
	key  i18n.Key
	args []msgArg
}

// say is a message of key with names as its arguments.
func say(key i18n.Key, args ...msgArg) message {
	return message{key: key, args: args}
}

// in writes the message in l.
func (m message) in(l i18n.Lang) string {
	return l.T(m.key, in(l, m.args)...)
}

// taskEvent is one step of a task. A line and the result are kept as
// messages: plain for everyone, and, when the step has engineering words
// (a Job's name, a commit, a revision), eng for people with deploy. A log
// chunk is kept as it came, and so is a failure's error, which only people
// with deploy see.
type taskEvent struct {
	kind  string
	plain message
	eng   *message
	text  string
	ok    bool
}

// say is the event's message for the viewer: the engineering one for
// people with deploy when there is one, else the plain one.
func (e taskEvent) say(l i18n.Lang, engineering bool) string {
	if engineering && e.eng != nil {
		return e.eng.in(l)
	}

	return e.plain.in(l)
}

// render is the event's HTML in l. The result leads back to the site at
// back and names support; a failure's error is in it only when engineering
// (the viewer has deploy).
//
//nolint:ireturn // each kind renders another component
func (e taskEvent) render(l i18n.Lang, engineering bool, back string, support ui.Support) templ.Component {
	switch e.kind {
	case eventLog:
		return ui.TaskText(e.text)
	case eventDone:
		o := ui.TaskOutcome{OK: e.ok, Text: e.say(l, engineering), Detail: "", Back: back, Support: support}
		if !e.ok && engineering {
			o.Detail = e.text
		}

		return ui.TaskResult(l, o)
	default:
		return ui.TaskStep(e.say(l, engineering))
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

// lineFor appends a line in plain words, which people with deploy read in
// engineering words instead.
func (t *task) lineFor(plain, eng message) {
	t.add(taskEvent{kind: eventLine, plain: plain, eng: &eng, text: "", ok: false})
}

// progress turns the operations' events into the task's: the deploy and Job
// lines into catalogue lines, the logs as they are. A Job's name is only for
// people with deploy; everyone else reads what the Job does. The
// operations' own end line is left out; the task writes its result itself.
func (t *task) progress() ops.Progress {
	return func(e ops.Event) {
		switch e.Kind {
		case ops.EventDeploying:
			t.lineFor(say(i18n.UpdatingSite, names(e.Site, e.Env)...), say(i18n.Deploying, names(e.Site, e.Env)...))
		case ops.EventJobStarted:
			plain := say(i18n.SavingContent)
			if t.action == ui.ActionRestore {
				plain = say(i18n.PuttingContentBack)
			}

			t.lineFor(plain, say(i18n.JobStarted, names(e.Job)...))
		case ops.EventLog:
			t.add(taskEvent{kind: eventLog, plain: message{key: 0, args: nil}, eng: nil, text: e.Text, ok: false})
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

// outcome is how a task's work ended, for its result line and the audit:
// on success done says it, with eng in engineering words for people with
// deploy when set; on failure fail names what failed in the person's words
// and err, which the audit keeps, says why.
type outcome struct {
	err    error
	fail   i18n.Key
	done   message
	eng    *message
	commit string
	detail string
}

// succeeded is an outcome that went well, said in plain words to everyone.
func succeeded(done message, commit, detail string) outcome {
	return outcome{err: nil, fail: i18n.TaskFailed, done: done, eng: nil, commit: commit, detail: detail}
}

// succeededFor is an outcome that went well, said in plain words, and in
// engineering words to people with deploy.
func succeededFor(done, eng message, commit, detail string) outcome {
	return outcome{err: nil, fail: i18n.TaskFailed, done: done, eng: &eng, commit: commit, detail: detail}
}

// failed is an outcome that failed at what fail names, because of err.
func failed(fail i18n.Key, err error, commit, detail string) outcome {
	return outcome{err: err, fail: fail, done: say(fail), eng: nil, commit: commit, detail: detail}
}

// start runs work in the background as a task of the person in sess, under
// the lock already taken, and redirects the browser to its progress page.
// The action is audited as started before it runs, so it is in the log even
// when the process dies under it; when work ends the lock is released, the
// result is written to the task and the action is audited again with it.
func (s *Server) start(
	c *echo.Context, sess session.Session, env, site string, action ui.Action,
	release func(), work func(context.Context, *task) outcome,
) error {
	t := s.tasks.create(env, site, action)
	s.metrics.running.Inc()

	started := s.record(sess, action, env, site)
	started.Result, started.Detail = audit.ResultStarted, "task "+t.id
	s.audit(c.Request().Context(), started)

	s.wg.Go(func() {
		defer s.metrics.running.Dec()
		defer release()

		out := work(s.base, t)
		rec := s.record(sess, action, env, site)
		rec.Commit, rec.Detail = out.commit, out.detail

		if out.err != nil {
			rec.Result, rec.Detail = audit.ResultFailed, joinDetail(out.detail, out.err.Error())

			key := out.fail
			if s.base.Err() != nil {
				key = i18n.FailedStopped
			}

			t.finish(taskEvent{kind: eventDone, plain: say(key), eng: nil, text: out.err.Error(), ok: false}, s.now())
		} else {
			rec.Result = audit.ResultOK
			t.finish(taskEvent{kind: eventDone, plain: out.done, eng: out.eng, text: "", ok: true}, s.now())
		}

		s.audit(s.base, rec)
	})

	return c.Redirect(http.StatusSeeOther, "/tasks/"+t.id)
}

// toLF turns the line ends SSE knows (CRLF and a bare CR as well as LF)
// into LF.
func toLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
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
	engineering := s.allows(sessionOf(c), t.env, t.site, access.Deploy)
	back := ui.SitePath(t.env, t.site)
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
			err := writeEvent(ctx, w, e.kind, e.render(l, engineering, back, s.support()))
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
// component, one data line per line of HTML. SSE ends a line at CR as well
// as LF, so every line end becomes its own data line: a CR left inside one
// would let a Job's log start a field of its own, such as a fake done event.
func writeEvent(ctx context.Context, w http.ResponseWriter, name string, comp templ.Component) error {
	var buf bytes.Buffer
	err := comp.Render(ctx, &buf)
	if err != nil {
		return err
	}

	var out strings.Builder

	out.WriteString("event: " + name + "\n")

	for line := range strings.Lines(toLF(buf.String())) {
		out.WriteString("data: " + strings.TrimSuffix(line, "\n") + "\n")
	}

	out.WriteString("\n")

	_, err = fmt.Fprint(w, out.String())
	if err != nil {
		return fmt.Errorf("writing an event: %w", err)
	}

	return nil
}
