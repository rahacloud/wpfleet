// Package audit is the record the server writes for every action, allowed or
// refused. A record becomes one JSON log line with "audit": true; the
// cluster's log collection keeps it, and the server keeps no database. The
// package is pure: it builds the attributes and the caller logs them.
package audit

import (
	"log/slog"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
)

// Message is the log message of every audit line.
const Message = "audit"

// The results a record carries.
const (
	// ResultOK is an action that was allowed and succeeded.
	ResultOK = "ok"
	// ResultDenied is an action the access rules refused.
	ResultDenied = "denied"
	// ResultFailed is an action that was allowed and failed.
	ResultFailed = "failed"
	// ResultBusy is an action refused because another one runs on the site.
	ResultBusy = "busy"
	// ResultStarted is an action that was accepted and began to run in the
	// background. Its end is a second record with ResultOK or ResultFailed,
	// so an action cut off by a killed process still left a line.
	ResultStarted = "started"
)

// Record is one audited action.
type Record struct {
	// Time is when the action was decided or finished; it becomes "at", since
	// the log handler writes its own "time".
	Time time.Time
	// Actor is the signed-in person. Only the subject, the email and the
	// groups are written; the display name is not.
	Actor access.Identity
	// Action names the action, e.g. deploy, promote, login, backup, restore.
	Action string
	// Env is the environment the action ran in.
	Env string
	// Site is the site the action ran on.
	Site string
	// Commit is the fleet commit a promote wrote or a deploy deployed.
	Commit string
	// Result is one of the Result constants.
	Result string
	// Detail is a short human-readable explanation, e.g. an error message.
	Detail string
	// WPUser is the WordPress user an admin login logged in as.
	WPUser string
	// Allowed is whether the access rules allowed the action.
	Allowed bool
}

// Attrs returns the record as log attributes, starting with audit=true.
// Empty optional fields (env, site, commit, detail, WordPress user) are left
// out so every line carries only what the action had.
func (r Record) Attrs() []slog.Attr {
	attrs := []slog.Attr{
		slog.Bool("audit", true),
		slog.String("action", r.Action),
		slog.Bool("allowed", r.Allowed),
		slog.String("result", r.Result),
		slog.GroupAttrs("actor", actor(r.Actor)...),
	}

	if !r.Time.IsZero() {
		attrs = append(attrs, slog.Time("at", r.Time.UTC()))
	}

	for _, optional := range []struct{ key, value string }{
		{"env", r.Env},
		{"site", r.Site},
		{"commit", r.Commit},
		{"detail", r.Detail},
		{"wpUser", r.WPUser},
	} {
		if optional.value != "" {
			attrs = append(attrs, slog.String(optional.key, optional.value))
		}
	}

	return attrs
}

// actor is the part of the identity an audit line names.
func actor(id access.Identity) []slog.Attr {
	groups := id.Groups
	if groups == nil {
		groups = []string{}
	}

	return []slog.Attr{
		slog.String("sub", id.Subject),
		slog.String("email", id.Email),
		slog.Any("groups", groups),
	}
}
