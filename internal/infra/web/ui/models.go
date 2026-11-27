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
	// CSRF is the session's token for the header's forms.
	CSRF string
	// Return is the path the language switch comes back to.
	Return string
}

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

// FleetView is the fleet grid: one row per site, one column per
// environment.
type FleetView struct {
	// Envs are the environments, in the fleet's order.
	Envs []string
	// Rows are the sites the person may see, by name.
	Rows []FleetRow
}

// FleetRow is one site across the environments.
type FleetRow struct {
	// Site is the site's name.
	Site string
	// Cells has one cell per environment, in the order of FleetView.Envs.
	Cells []FleetCell
}

// FleetCell is one site in one environment.
type FleetCell struct {
	// Env and Site name the cell.
	Env, Site string
	// Visible says whether the person may see the site in this environment;
	// a cell they may not see is empty.
	Visible bool
	// Present says whether the environment has the site.
	Present bool
	// Tag is the image tag the environment's values deploy.
	Tag string
	// Ready is ready pods out of running ones, or empty when unknown.
	Ready string
	// NewerIn names the environment this one promotes from when that one
	// runs another tag, so a pending promotion stands out.
	NewerIn string
}

// Drift is whether a site's live release matches what the branch renders.
type Drift int

// The drift states.
const (
	// DriftUnknown is a release that could not be compared.
	DriftUnknown Drift = iota
	// DriftNone is a release that matches the branch.
	DriftNone
	// DriftChanged is a release that differs from the branch.
	DriftChanged
	// DriftNotInstalled is a site without a release.
	DriftNotInstalled
)

// CellStatus is what the fleet grid loads into a cell after the page: the
// commit, the drift and the last backup.
type CellStatus struct {
	// Engineering shows the commit and the drift, for people with deploy.
	Engineering bool
	// Commit is the short commit the release was deployed from; empty when
	// unknown.
	Commit string
	// Dirty says the release was deployed from uncommitted changes.
	Dirty bool
	// Drift says whether the release matches the branch.
	Drift Drift
	// BackupEnabled says whether the site's backups are on.
	BackupEnabled bool
	// LastBackup is when a backup last succeeded, if ever.
	LastBackup *time.Time
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

// SiteView is one site in one environment.
type SiteView struct {
	// Env and Site name the site.
	Env, Site string
	// Host is the site's address and URL its link.
	Host, URL string
	// Image is the full image and Tag its tag.
	Image, Tag string
	// PromoteFrom is the environment this one publishes from; empty when
	// none.
	PromoteFrom string
	// FromTag is the tag in PromoteFrom.
	FromTag string
	// CanPublish shows "Publish <from> to <env>": deploy here and view in
	// PromoteFrom.
	CanPublish bool
	// Perms are the person's actions on the site.
	Perms Perms
	// Engineering shows the release, pods, history and diff, for people
	// with deploy.
	Engineering bool
	// Running is the action in progress on the site; empty when none.
	Running Action
	// Unavailable says the cluster could not be read; the rest still shows.
	Unavailable bool
	// Ready is ready pods out of running ones.
	Ready string
	// Pods are the site's pods.
	Pods []Pod
	// Release is the installed release; nil when the site is not installed.
	Release *Release
	// Volume is the uploads volume.
	Volume Volume
	// Backup is the state of the backup CronJob.
	Backup BackupState
	// CSRF is the session's token for the action forms.
	CSRF string
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

// BackupsView is the list of a site's backups.
type BackupsView struct {
	// Env and Site name the site.
	Env, Site string
	// CSRF is the session's token for the restore forms.
	CSRF string
	// CanRestore shows the restore forms.
	CanRestore bool
	// Failed says the backups could not be listed.
	Failed bool
	// Rows are the backups, newest first.
	Rows []BackupRow
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

// TaskView is the progress page of a running action.
type TaskView struct {
	// Env and Site name the site.
	Env, Site string
	// Action is what runs.
	Action Action
	// EventsURL is the server-sent events stream of the task.
	EventsURL string
}

// Message is a page that says one thing: a refusal, an error, sign-out.
type Message struct {
	// Title heads the page.
	Title string
	// Text is the message.
	Text string
	// Bad marks a refusal or an error.
	Bad bool
	// Link and LinkText lead on; no link when Link is empty.
	Link, LinkText string
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
