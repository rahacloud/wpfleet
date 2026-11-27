// Package ops holds wpfleet's operations, shared by the CLI and the server:
// deploy, diff, promote, the one-time admin login, the sites list and detail,
// and backup run, list and restore. Each takes an environment (or the fleet),
// the sites and an options struct, returns typed results, and reports
// progress through a Progress callback instead of writing to a terminal, so
// the CLI prints it and the server streams it. Printing, flags and
// confirmation questions stay with the callers.
package ops

import (
	"bytes"
	"sync"
)

// EventKind says what an Event reports.
type EventKind string

// The kinds of events.
const (
	// EventDeploying: a deploy of Site starts.
	EventDeploying EventKind = "deploying"
	// EventJobStarted: a Job named Job was created for Site.
	EventJobStarted EventKind = "job-started"
	// EventLog: Text is a chunk of a Job's logs, or the "==> pod/container
	// <==" header before a container's log. Chunks are not split at lines.
	EventLog EventKind = "log"
	// EventDone: the operation on Site finished; Text says how.
	EventDone EventKind = "done"
)

// Event is one step of an operation's progress.
type Event struct {
	// Kind says what the event reports.
	Kind EventKind `json:"kind"`
	// Env is the environment.
	Env string `json:"env"`
	// Site is the site.
	Site string `json:"site"`
	// Job is the Job's name, for Job events.
	Job string `json:"job,omitempty"`
	// Namespace is the Job's namespace, for Job events.
	Namespace string `json:"namespace,omitempty"`
	// Text is the log chunk of an EventLog, and for the other kinds the
	// English line the CLI prints for people.
	Text string `json:"text"`
}

// Progress receives an operation's events, in order, from one goroutine at a
// time. A nil Progress drops them.
type Progress func(Event)

// emit sends e to p unless p is nil.
func (p Progress) emit(e Event) {
	if p != nil {
		p(e)
	}
}

// logWriter turns what is written to it into EventLog events.
type logWriter struct {
	mu       sync.Mutex
	progress Progress
	env      string
	site     string
	job      string
}

// newLogWriter is a writer whose writes become EventLog events of a Job.
func newLogWriter(progress Progress, env, site, job string) *logWriter {
	return &logWriter{mu: sync.Mutex{}, progress: progress, env: env, site: site, job: job}
}

// Write emits b as one EventLog.
func (w *logWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.progress.emit(Event{
		Kind: EventLog, Env: w.env, Site: w.site, Job: w.job, Namespace: "", Text: string(bytes.Clone(b)),
	})

	return len(b), nil
}
