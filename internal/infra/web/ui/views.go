package ui

import (
	"strconv"
	"strings"
	"time"
)

// Health is how a site is doing, in the words of the people who run its
// content: online, starting or down, from the readiness of its pods.
type Health int

// The health states.
const (
	// HealthUnknown is a site whose cluster could not be read.
	HealthUnknown Health = iota
	// HealthOnline is a site whose pods are all ready.
	HealthOnline
	// HealthStarting is a site with some pods ready and some not, as during
	// a rollout.
	HealthStarting
	// HealthDown is a site with no ready pod.
	HealthDown
)

// HealthOf reads a readiness written as ready pods out of running ones
// ("1/1"): all ready is online, some is starting, none (or no pods) is
// down, and anything else is unknown.
func HealthOf(ready string) Health {
	up, all, ok := strings.Cut(ready, "/")
	if !ok {
		return HealthUnknown
	}

	a, errA := strconv.Atoi(up)
	b, errB := strconv.Atoi(all)

	switch {
	case errA != nil || errB != nil || a < 0 || a > b:
		return HealthUnknown
	case b == 0 || a == 0:
		return HealthDown
	case a == b:
		return HealthOnline
	default:
		return HealthStarting
	}
}

// Support is whom a person asks for help; both fields are optional.
type Support struct {
	// Email is the support team's address.
	Email string
	// URL is the support team's page.
	URL string
}

// Link is where "Contact support" leads: the page when there is one, else a
// mail to the address, else empty.
func (s Support) Link() string {
	switch {
	case s.URL != "":
		return s.URL
	case s.Email != "":
		return "mailto:" + s.Email
	default:
		return ""
	}
}

// SearchThreshold is the number of sites above which the home page offers a
// search box.
const SearchThreshold = 6

// HomeView is the home page: one card per site the person may see.
type HomeView struct {
	// Cards are the sites that match Query, by name.
	Cards []SiteCard
	// Total counts the person's sites before the search.
	Total int
	// Query is the search; empty shows every site.
	Query string
	// CSRF is the session's token for the cards' forms.
	CSRF string
	// Support is whom to ask when the person has no sites.
	Support Support
}

// Searchable says whether the page shows the search box: with more sites
// than SearchThreshold, or when a search is already in the address.
func (v HomeView) Searchable() bool {
	return v.Total > SearchThreshold || v.Query != ""
}

// SiteCard is one site across the environments the person may see.
type SiteCard struct {
	// Site is the site's name.
	Site string
	// Envs are the environments that have the site and that the person may
	// see, the live one first and then back along the promoteFrom chain.
	Envs []CardEnv
	// Publish offers to publish a newer version into an environment; nil
	// when none waits or the person may not publish it.
	Publish *PublishOffer
}

// Primary is the environment the card leads with: the live one, or the one
// closest to it.
func (c SiteCard) Primary() CardEnv {
	if len(c.Envs) == 0 {
		return CardEnv{Env: "", Live: false, Health: HealthUnknown, Host: "", URL: "", CanLogin: false}
	}

	return c.Envs[0]
}

// CardEnv is one site in one environment on its card.
type CardEnv struct {
	// Env names the environment.
	Env string
	// Live says this is the environment visitors see.
	Live bool
	// Health is how the site is doing there.
	Health Health
	// Host is the site's address there and URL its link; both empty when the
	// site has no address.
	Host, URL string
	// CanLogin shows "Open admin".
	CanLogin bool
}

// PublishOffer is "Publish <From> to <To>" on a card.
type PublishOffer struct {
	// From is the environment with the newer version; To the one it goes to.
	From, To string
}

// BackupStatus is the last backup of a site, loaded into its card after the
// page.
type BackupStatus struct {
	// Enabled says whether the site's backups are on.
	Enabled bool
	// Last is when a backup last succeeded, if ever.
	Last *time.Time
	// Now is the time the page is written at.
	Now time.Time
}

// SiteView is one site in one environment.
type SiteView struct {
	// Env and Site name the site.
	Env, Site string
	// Live says the environment is the one visitors see.
	Live bool
	// Host is the site's address and URL its link.
	Host, URL string
	// Health is how the site is doing.
	Health Health
	// Image is the full image and Tag its tag.
	Image, Tag string
	// PromoteFrom is the environment this one publishes from; empty when
	// none.
	PromoteFrom string
	// FromTag is the tag in PromoteFrom.
	FromTag string
	// CanPublish shows "Publish <from> to <env>": deploy here, view in
	// PromoteFrom, and another version there.
	CanPublish bool
	// Perms are the person's actions on the site.
	Perms Perms
	// Engineering shows the technical details, for people with deploy.
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
	// Releases are the site's releases, newest first.
	Releases []ReleaseEntry
	// Volume is the uploads volume.
	Volume Volume
	// Backup is the state of the backup CronJob.
	Backup BackupState
	// Others are the other environments that have the site and that the
	// person may see, live first.
	Others []EnvLink
	// CSRF is the session's token for the action forms.
	CSRF string
	// Now is the time the page is written at.
	Now time.Time
}

// ReleaseEntry is one release of a site, for the release history.
type ReleaseEntry struct {
	// Revision is Helm's revision number.
	Revision int
	// Updated is when the revision was deployed.
	Updated time.Time
	// Status is Helm's status of the revision.
	Status string
	// Commit is the short commit the revision was deployed from; empty when
	// unknown.
	Commit string
	// Author is the Git author of that commit; empty when unknown.
	Author string
}

// EnvLink leads to a site in another environment.
type EnvLink struct {
	// Env names the environment.
	Env string
	// Live says it is the environment visitors see.
	Live bool
}

// BackupsView is the list of a site's backups on its page.
type BackupsView struct {
	// Off says the site's backups are turned off; backups taken before
	// still show.
	Off bool
	// Failed says the backups could not be listed.
	Failed bool
	// Rows are the backups, newest first.
	Rows []BackupRow
	// Now is the time the list is written at.
	Now time.Time
}

// RestoreView is the restore page: the backups to choose from, what a
// restore replaces, and the typed confirmation.
type RestoreView struct {
	// Env and Site name the site.
	Env, Site string
	// CSRF is the session's token for the form.
	CSRF string
	// Backups are the site's backups.
	Backups BackupsView
}

// Restorable says whether any backup can be restored.
func (v RestoreView) Restorable() bool {
	for _, r := range v.Backups.Rows {
		if r.HasDump {
			return true
		}
	}

	return false
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

// TaskOutcome is how an action ended, as its progress page shows it.
type TaskOutcome struct {
	// OK says the action succeeded.
	OK bool
	// Text says what happened, in the viewer's words.
	Text string
	// Detail is the error behind a failure, for people with deploy only.
	Detail string
	// Back is the site's page.
	Back string
	// Support is whom to ask when it failed.
	Support Support
}

// Message is a page that says one thing: a refusal, an error, sign-out.
type Message struct {
	// Title heads the page.
	Title string
	// Text is the message.
	Text string
	// Hint says what the person can do about it; empty for none.
	Hint string
	// Bad marks a refusal or an error.
	Bad bool
	// Link and LinkText lead on; no link when Link is empty.
	Link, LinkText string
	// Support is whom to ask; shown on a refusal or an error.
	Support Support
}
