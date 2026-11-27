// Package ui holds the templ components of wpfleet's web UI and the static
// files they load. The components take plain view models, built by the web
// package from the operations' results and the person's permissions, and a
// language from the i18n package for every text they show; they decide no
// permission themselves.
package ui

import (
	"net/url"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/web/ui/i18n"
)

//go:generate go tool templ generate

// Page is what the layout of every page needs.
type Page struct {
	// L is the language the page is written in.
	L i18n.Lang
	// Title is the page's title.
	Title string
	// User names the signed-in person; empty on a page shown before sign-in
	// or after sign-out, which then has no header forms.
	User string
	// UserEmail is the signed-in person's email, shown when User is hovered.
	UserEmail string
	// CSRF is the session's token for the header's forms.
	CSRF string
	// Return is the path the language switch comes back to.
	Return string
	// LangLinks switch the language of a page shown without a session,
	// which has no session to keep the choice in.
	LangLinks []LangLink
	// Brand is the fleet's own name, logo and color.
	Brand Brand
	// Version is wpfleet's version, for the footer.
	Version string
}

// LangLink is a link to the same page in another language.
type LangLink struct {
	// L is the language the link leads to.
	L i18n.Lang
	// URL is the page's path with the language in its query.
	URL string
}

// Brand is the fleet's own look, as the layout shows it.
type Brand struct {
	// Name is shown beside the logo and in the title.
	Name string
	// LogoURL is the logo's address; empty shows the name alone.
	LogoURL string
	// FaviconURL and FaviconType are the favicon's address and media type.
	FaviconURL, FaviconType string
	// ThemeURL is the stylesheet with the brand's colors.
	ThemeURL string
	// SupportURL is where "Need help?" leads, an https or mailto URL; empty
	// shows no link.
	SupportURL string
	// Welcome is the home page's short text in the page's language; empty
	// shows none.
	Welcome string
}

// Repository is wpfleet's source code, which the footer of every page
// links whatever the brand.
const Repository = "https://github.com/rahacloud/wpfleet"

// LicenseURL is wpfleet's license.
const LicenseURL = Repository + "/blob/main/LICENSE"

// Action is an operation a person starts from the UI.
type Action string

// The actions of the UI.
const (
	// ActionDeploy deploys what the branch holds.
	ActionDeploy Action = "deploy"
	// ActionPromote publishes one environment's version into the next and
	// deploys it.
	ActionPromote Action = "promote"
	// ActionLogin opens a site's admin with a one-time link.
	ActionLogin Action = "login"
	// ActionBackup takes a backup now.
	ActionBackup Action = "backup"
	// ActionRestore restores a backup.
	ActionRestore Action = "restore"
)

// Label names the action in l.
func (a Action) Label(l i18n.Lang) string {
	switch a {
	case ActionDeploy:
		return l.T(i18n.ActionDeploy)
	case ActionPromote:
		return l.T(i18n.ActionPromote)
	case ActionLogin:
		return l.T(i18n.ActionLogin)
	case ActionBackup:
		return l.T(i18n.ActionBackup)
	case ActionRestore:
		return l.T(i18n.ActionRestore)
	default:
		return string(a)
	}
}

// Perms are the actions a person may start on a site; a button they may
// not use is not shown.
type Perms struct {
	// Deploy allows deploy and publishing into the environment.
	Deploy bool
	// Login allows the one-time admin login.
	Login bool
	// Backup allows a backup now.
	Backup bool
	// Restore allows restoring a backup.
	Restore bool
}

// Pod is one pod of a site.
type Pod struct {
	// Name, Node and Phase describe the pod.
	Name, Node, Phase string
	// Ready says whether the pod is ready.
	Ready bool
}

// Release is a site's installed release.
type Release struct {
	// Revision is Helm's revision number.
	Revision int
	// Status is Helm's status.
	Status string
	// Updated is when the revision was deployed.
	Updated time.Time
	// Commit is the short commit the revision was deployed from; empty when
	// it was not deployed by wpfleet.
	Commit string
	// Dirty says the revision was deployed from uncommitted changes.
	Dirty bool
}

// Volume is a site's uploads claim.
type Volume struct {
	// Claim is the claim's name.
	Claim string
	// Found says whether it exists.
	Found bool
	// Phase, Capacity and StorageClass describe it.
	Phase, Capacity, StorageClass string
}

// BackupState is the state of a site's backup CronJob.
type BackupState struct {
	// Enabled says whether backups are on.
	Enabled bool
	// LastSuccess is when a backup last succeeded, if ever.
	LastSuccess *time.Time
}

// HistoryEntry is one commit that changed a site's file.
type HistoryEntry struct {
	// SHA is the short commit.
	SHA string
	// Author names who made the change.
	Author string
	// When is the commit's time.
	When time.Time
	// Subject is the commit's first line.
	Subject string
}

// DiffView is what a deploy would change.
type DiffView struct {
	// Installed says whether the site has a release.
	Installed bool
	// Changed says whether a deploy changes anything.
	Changed bool
	// Diff is the unified diff, secrets redacted.
	Diff string
	// Error is why the diff could not be computed.
	Error string
}

// BackupRow is one backup.
type BackupRow struct {
	// Stamp names the backup, as the restore form sends it.
	Stamp string
	// Taken is when it ran.
	Taken time.Time
	// HasDump says whether the database dump is there; only a backup with a
	// dump can be restored.
	HasDump bool
	// DumpSize is the dump's size in bytes.
	DumpSize int64
	// ChangedFiles counts the upload files the run replaced or deleted.
	ChangedFiles int
	// Expires is when the backup is deleted.
	Expires time.Time
}

// SitePath is the path of a site's page.
func SitePath(env, site string) string {
	return "/sites/" + url.PathEscape(env) + "/" + url.PathEscape(site)
}

// ActionPath is the path a site's action, or one of its fragments, is at.
func ActionPath(env, site, action string) string {
	return SitePath(env, site) + "/" + action
}

// shortSHA is how many hex digits of a commit the UI shows.
const shortSHA = 8

// ShortSHA cuts a commit to the digits the UI shows.
func ShortSHA(sha string) string {
	if len(sha) > shortSHA {
		return sha[:shortSHA]
	}

	return sha
}
