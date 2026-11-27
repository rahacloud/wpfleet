package ui

import (
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

// readyOrUnknown is a readiness, or "unknown" in l for an empty one.
func readyOrUnknown(l i18n.Lang, ready string) string {
	if ready == "" {
		return l.T(i18n.Unknown)
	}

	return ready
}

// lastBackupText says when a backup last succeeded, that none ever did, or
// that backups are off.
func lastBackupText(l i18n.Lang, enabled bool, last *time.Time) string {
	switch {
	case !enabled:
		return l.T(i18n.BackupsOff)
	case last == nil:
		return l.T(i18n.Never)
	default:
		return l.Time(*last)
	}
}

// yesNo is yes or no in l.
func yesNo(l i18n.Lang, b bool) string {
	if b {
		return l.T(i18n.Yes)
	}

	return l.T(i18n.No)
}
