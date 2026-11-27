package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

// tagOrUnknown is a tag, or "unknown" in l for an empty one.
func tagOrUnknown(l i18n.Lang, tag string) string {
	if tag == "" {
		return l.T(i18n.Unknown)
	}

	return tag
}

// readyOrUnknown is a readiness ("1/1") in the digits of l, or "unknown" in
// l for an empty one.
func readyOrUnknown(l i18n.Lang, ready string) string {
	if ready == "" {
		return l.T(i18n.Unknown)
	}

	up, all, ok := strings.Cut(ready, "/")
	a, errA := strconv.Atoi(up)
	b, errB := strconv.Atoi(all)

	if !ok || errA != nil || errB != nil {
		return ready
	}

	return l.Int(a) + "/" + l.Int(b)
}

// backupText says in words when a backup last succeeded, that none did yet,
// or that backups are off.
func backupText(l i18n.Lang, enabled bool, last *time.Time, now time.Time) string {
	switch {
	case !enabled:
		return l.T(i18n.BackupsOff)
	case last == nil:
		return l.T(i18n.NotBackedUpYet)
	default:
		return l.T(i18n.BackedUpAgo, l.Ago(*last, now))
	}
}

// backupBadge is the badge class of a site's backup state: a site never
// backed up while backups are on is a warning.
func backupBadge(enabled bool, last *time.Time) string {
	if enabled && last == nil {
		return "wf-badge wf-badge-warn"
	}

	return "wf-badge"
}

// healthText names a health in l.
func healthText(l i18n.Lang, h Health) string {
	switch h {
	case HealthOnline:
		return l.T(i18n.StatusOnline)
	case HealthStarting:
		return l.T(i18n.StatusStarting)
	case HealthDown:
		return l.T(i18n.StatusDown)
	case HealthUnknown:
	}

	return l.T(i18n.StatusUnknown)
}

// healthBadge is the badge class of a health. The text always says the
// state as well, so the color is never the only sign.
func healthBadge(h Health) string {
	switch h {
	case HealthOnline:
		return "wf-badge wf-badge-ok"
	case HealthStarting:
		return "wf-badge wf-badge-warn"
	case HealthDown:
		return "wf-badge wf-badge-bad"
	case HealthUnknown:
	}

	return "wf-badge"
}

// healthMark is a sign in front of a health's text, so the states differ in
// shape too, for people who do not see the colors.
func healthMark(h Health) string {
	switch h {
	case HealthOnline:
		return "●"
	case HealthStarting:
		return "◐"
	case HealthDown:
		return "○"
	case HealthUnknown:
	}

	return "?"
}

// yesNo is yes or no in l.
func yesNo(l i18n.Lang, b bool) string {
	if b {
		return l.T(i18n.Yes)
	}

	return l.T(i18n.No)
}

// siteCount counts sites in l.
func siteCount(l i18n.Lang, n int) string {
	return l.Count(n, i18n.SiteCountOne, i18n.SiteCountMany)
}

// newTab is a link's accessible name that says it opens a new tab.
func newTab(l i18n.Lang, text string) string {
	return l.T(i18n.OpensInNewTab, text)
}

// taskHeading heads the progress page of an action in plain words.
func taskHeading(l i18n.Lang, t TaskView) string {
	switch t.Action {
	case ActionDeploy:
		return l.T(i18n.TaskHeadingDeploy, t.Site, t.Env)
	case ActionPromote:
		return l.T(i18n.TaskHeadingPromote, t.Site, t.Env)
	case ActionBackup:
		return l.T(i18n.TaskHeadingBackup, t.Site, t.Env)
	case ActionRestore:
		return l.T(i18n.TaskHeadingRestore, t.Site, t.Env)
	case ActionLogin:
		return l.T(i18n.TaskTitle, t.Action.Label(l), t.Site, t.Env)
	default:
		return l.T(i18n.TaskTitle, t.Action.Label(l), t.Site, t.Env)
	}
}

// stampID is the id of a backup's radio button.
func stampID(stamp string) string {
	return "backup-" + stamp
}

// messageRole is the ARIA role of a message: an alert for a refusal or an
// error, a status otherwise.
func messageRole(bad bool) string {
	if bad {
		return "alert"
	}

	return "status"
}
