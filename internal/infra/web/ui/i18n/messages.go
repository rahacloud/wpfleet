package i18n

// Key names one message of the catalogue.
type Key int

// The messages of the UI. A %s in a message is filled by the arguments of T,
// in order.
const (
	// SignOut is the button that ends the session.
	SignOut Key = iota
	// LanguageLabel labels the language switch.
	LanguageLabel
	// SitesTitle heads the fleet page.
	SitesTitle
	// SiteColumn heads the site column of the fleet grid.
	SiteColumn
	// NoSites says that the person has no sites to see.
	NoSites
	// NotInEnvironment marks a site that an environment does not have.
	NotInEnvironment
	// ReadyLabel labels a site's readiness: ready pods out of running ones.
	ReadyLabel
	// Unknown is a value that could not be read.
	Unknown
	// NewerIn says that the environment a site promotes from runs another
	// version (%s: that environment).
	NewerIn
	// Loading stands in for a part of the page that is still loading.
	Loading
	// CommitLabel labels the Git commit a release runs.
	CommitLabel
	// UncommittedChanges marks a release deployed from a tree with changes
	// that were not committed.
	UncommittedChanges
	// DriftLabel labels whether the live release differs from Git.
	DriftLabel
	// DriftNone says the live release matches Git.
	DriftNone
	// DriftChanged says the live release differs from Git.
	DriftChanged
	// DriftNotInstalled says the site has no release.
	DriftNotInstalled
	// DriftUnknown says the release could not be compared with Git.
	DriftUnknown
	// LastBackup labels when a backup last succeeded.
	LastBackup
	// Never says a backup never succeeded.
	Never
	// BackupsOff says a site's backups are turned off.
	BackupsOff
	// AddressLabel labels a site's address.
	AddressLabel
	// VersionLabel labels the version (image tag) a site runs.
	VersionLabel
	// ImageLabel labels the full image of a site.
	ImageLabel
	// PodsTitle heads the list of a site's pods.
	PodsTitle
	// PodLabel heads the pod name column.
	PodLabel
	// NodeLabel heads the node column.
	NodeLabel
	// StateLabel heads a state column.
	StateLabel
	// Yes is an affirmative cell.
	Yes
	// No is a negative cell.
	No
	// NoPods says the site has no pods.
	NoPods
	// ReleaseTitle heads the release of a site.
	ReleaseTitle
	// RevisionLabel labels a release's revision.
	RevisionLabel
	// UpdatedLabel labels when something was last updated.
	UpdatedLabel
	// NotInstalled says the site has no release.
	NotInstalled
	// VolumeLabel labels the site's uploads volume.
	VolumeLabel
	// VolumeMissing says the uploads volume was not found.
	VolumeMissing
	// HistoryTitle heads the Git history of the site's file.
	HistoryTitle
	// AuthorLabel heads the author column.
	AuthorLabel
	// WhenLabel heads a time column.
	WhenLabel
	// ChangeLabel heads the column of a change's subject.
	ChangeLabel
	// NoHistory says the site's file has no history.
	NoHistory
	// HistoryUnavailable says the history could not be read.
	HistoryUnavailable
	// DiffTitle heads the difference between the cluster and Git.
	DiffTitle
	// NoDiff says a deploy would change nothing.
	NoDiff
	// DiffUnavailable says the difference could not be computed (%s: why).
	DiffUnavailable
	// BackupsTitle heads the list of backups.
	BackupsTitle
	// TakenLabel heads the column of when a backup was taken.
	TakenLabel
	// DatabaseLabel heads the column of the database dump's size.
	DatabaseLabel
	// ChangedFilesLabel heads the column of changed upload files.
	ChangedFilesLabel
	// KeptUntilLabel heads the column of when a backup is deleted.
	KeptUntilLabel
	// NoBackups says there are no backups.
	NoBackups
	// BackupsUnavailable says the backups could not be listed.
	BackupsUnavailable
	// OpenAdmin opens the site's admin.
	OpenAdmin
	// Publish promotes from one environment to another and deploys (%s:
	// from, %s: to).
	Publish
	// DeployButton deploys what Git holds.
	DeployButton
	// BackUpNow starts a backup.
	BackUpNow
	// RestoreButton restores a backup.
	RestoreButton
	// RestoreUploads asks to restore the uploaded files too.
	RestoreUploads
	// RestoreConfirm asks for the site's name (%s) before a restore.
	RestoreConfirm
	// RestoreMismatch says the typed name was not the site's.
	RestoreMismatch
	// ActionDeploy names the deploy action.
	ActionDeploy
	// ActionPromote names the publish action.
	ActionPromote
	// ActionLogin names the admin login.
	ActionLogin
	// ActionBackup names the backup action.
	ActionBackup
	// ActionRestore names the restore action.
	ActionRestore
	// Busy refuses an action while another runs (%s: action, %s: site,
	// %s: environment).
	Busy
	// RunningNow says which action is running on the site (%s).
	RunningNow
	// RefusedTitle heads a refusal page.
	RefusedTitle
	// Forbidden says the person may not do this.
	Forbidden
	// NoAccessTitle heads the page of a person without any access.
	NoAccessTitle
	// NoAccess explains that the person has no access at all.
	NoAccess
	// ExpiredForm says the form's CSRF token did not match.
	ExpiredForm
	// NotFoundTitle heads the page of a missing page.
	NotFoundTitle
	// NotFound says there is no such page.
	NotFound
	// ErrorTitle heads an error page.
	ErrorTitle
	// ServerError says something failed on the server.
	ServerError
	// BadRequest says the request was not understood.
	BadRequest
	// SignedOutTitle heads the page after signing out.
	SignedOutTitle
	// SignedOut says the person signed out.
	SignedOut
	// SignIn starts a new sign-in.
	SignIn
	// SignInFailed says signing in failed.
	SignInFailed
	// Back leads back to the site's page.
	Back
	// TaskTitle heads the progress page (%s: action, %s: site, %s:
	// environment).
	TaskTitle
	// Working says the action is still running.
	Working
	// TaskDone says the action finished.
	TaskDone
	// TaskFailed says the action failed, when no reason below fits.
	TaskFailed
	// FailedSync says reading the branch failed.
	FailedSync
	// FailedPublish says committing the promotion failed.
	FailedPublish
	// FailedDeploy says the deploy failed.
	FailedDeploy
	// FailedBackup says the backup failed.
	FailedBackup
	// FailedRestore says the restore failed.
	FailedRestore
	// FailedStopped says the server stopped before the action ended.
	FailedStopped
	// FailureDetail gives the engineering reason of a failure to people with
	// deploy (%s: the error).
	FailureDetail
	// Deploying says a site is being deployed (%s: site, %s: environment).
	Deploying
	// JobStarted says a Job started (%s: its name).
	JobStarted
	// Published says the promotion was committed (%s: the commit).
	Published
	// AlreadyPublished says the target already runs the version, so it is
	// only deployed again.
	AlreadyPublished
	// Syncing says the server is reading the branch.
	Syncing
	// Deployed says a deploy finished, in engineering words (%s: revision).
	Deployed
	// Restored says a restore finished.
	Restored
	// BackedUp says a backup finished.
	BackedUp
	// UnknownSite says the site is not in the environment.
	UnknownSite
	// NoPromoteFrom says the environment has nothing to publish from.
	NoPromoteFrom
	// SkipToContent is the first link of every page, for keyboards and
	// screen readers.
	SkipToContent
	// AccountMenu names the header's links and buttons for screen readers.
	AccountMenu
	// NeedHelp leads to the fleet's support.
	NeedHelp
	// PoweredBy comes before wpfleet's name in the footer.
	PoweredBy
	// LicenseName names wpfleet's license in the footer.
	LicenseName
	// SourceCode names the link to wpfleet's repository for screen readers.
	SourceCode
	// SignInHeading heads the sign-in page (%s: the fleet's name).
	SignInHeading
	// SignInIntro says what the server is for, to someone who has not
	// signed in.
	SignInIntro
	// SignInButton starts the sign-in.
	SignInButton
	// SignInHint says which account to use.
	SignInHint
	// SignInTrouble asks whether signing in fails, before the support link.
	SignInTrouble
	// ContactSupport is the link to the fleet's support.
	ContactSupport

	// JustNow is a time less than a minute ago.
	JustNow
	// MinuteAgo is one minute ago (%s: the number).
	MinuteAgo
	// MinutesAgo is several minutes ago (%s: the number).
	MinutesAgo
	// HourAgo is one hour ago (%s: the number).
	HourAgo
	// HoursAgo is several hours ago (%s: the number).
	HoursAgo
	// DayAgo is one day ago (%s: the number).
	DayAgo
	// DaysAgo is several days ago (%s: the number).
	DaysAgo
	// TodayAt is a time today (%s: the clock).
	TodayAt
	// YesterdayAt is a time yesterday (%s: the clock).
	YesterdayAt
	// DateAt is a time on another day (%s: the date, %s: the clock).
	DateAt
	// HomeTitle heads the home page.
	HomeTitle
	// SiteCountOne counts one site (%s: the number).
	SiteCountOne
	// SiteCountMany counts sites (%s: the number).
	SiteCountMany
	// SitesMatching says how many sites match a search (%s: matching, %s:
	// all).
	SitesMatching
	// SearchLabel labels the search box.
	SearchLabel
	// SearchHint explains what the search box finds.
	SearchHint
	// SearchButton submits the search.
	SearchButton
	// ShowAllSites clears the search.
	ShowAllSites
	// NoMatch says no site matches the search (%s: the search).
	NoMatch
	// ViewSite opens the public site.
	ViewSite
	// OpensInNewTab tells a screen reader that a link opens a new tab (%s:
	// the link's text).
	OpensInNewTab
	// StatusOnline says the site answers.
	StatusOnline
	// StatusStarting says some of the site's pods are not ready yet.
	StatusStarting
	// StatusDown says the site does not run.
	StatusDown
	// StatusUnknown says the site's state could not be read.
	StatusUnknown
	// LiveBadge marks the environment visitors see.
	LiveBadge
	// BackedUpAgo says when the last backup succeeded (%s: how long ago).
	BackedUpAgo
	// NotBackedUpYet says no backup succeeded yet.
	NotBackedUpYet
	// NewerWaiting says a newer version waits in the previous environment
	// (%s: that environment).
	NewerWaiting
	// NoSitesYet is the home page of a person with no sites.
	NoSitesYet
	// AskAdmin says whom to ask when no support contact is configured.
	AskAdmin
	// OpenAdminOf names the admin button for a screen reader (%s: site, %s:
	// environment).
	OpenAdminOf
	// OpenAdminHelp explains the admin button.
	OpenAdminHelp
	// AdminBlocked says why the admin button is off (%s: the action that
	// runs).
	AdminBlocked
	// ViewSiteHelp explains the site link.
	ViewSiteHelp
	// BackUpNowHelp explains the backup button.
	BackUpNowHelp
	// RestoreBackup leads to the restore page.
	RestoreBackup
	// RestoreBackupHelp explains the restore button.
	RestoreBackupHelp
	// PublishHelp explains the publish button (%s: from, %s: to).
	PublishHelp
	// DeployHelp explains the deploy button.
	DeployHelp
	// StatusLabel labels a site's state.
	StatusLabel
	// TechnicalDetails heads the engineering part of the site page.
	TechnicalDetails
	// ReleasesTitle heads the release history.
	ReleasesTitle
	// NoReleases says the site has no releases.
	NoReleases
	// OtherEnvironments leads to the site in the other environments.
	OtherEnvironments
	// NoAddress says the site has no address.
	NoAddress
	// DatabaseSize is the size of a backup's database copy (%s: the size).
	DatabaseSize
	// KeptUntil says when a backup is deleted (%s: the date).
	KeptUntil
	// NotRestorable marks a backup without a database copy.
	NotRestorable
	// RestoreTitle heads the restore page (%s: the site).
	RestoreTitle
	// RestoreIntro explains what a restore replaces.
	RestoreIntro
	// RestoreWarning says a restore cannot be undone.
	RestoreWarning
	// ChooseBackup labels the list of backups to restore.
	ChooseBackup
	// RestoreUploadsHelp explains restoring the uploaded files.
	RestoreUploadsHelp
	// RestoreConfirmLabel asks for the site's name (%s).
	RestoreConfirmLabel
	// RestoreSubmit starts the restore.
	RestoreSubmit
	// NoRestorable says there is no backup to restore.
	NoRestorable
	// Cancel leads back without doing anything.
	Cancel
	// TaskHeadingDeploy heads a deploy's progress (%s: site, %s: env).
	TaskHeadingDeploy
	// TaskHeadingPromote heads a publication's progress (%s: site, %s:
	// env).
	TaskHeadingPromote
	// TaskHeadingBackup heads a backup's progress (%s: site, %s: env).
	TaskHeadingBackup
	// TaskHeadingRestore heads a restore's progress (%s: site, %s: env).
	TaskHeadingRestore
	// TaskWait says the action takes a while and goes on without the page.
	TaskWait
	// ProgressTitle heads the steps of an action.
	ProgressTitle
	// ShowLog opens the raw log of an action.
	ShowLog
	// NextSuccess says what to do after an action succeeded.
	NextSuccess
	// NextFailure says what to do after an action failed.
	NextFailure
	// AskForAccess says whom to ask for a permission.
	AskForAccess
	// GoHome leads back to the home page.
	GoHome
	// BackupsUnknown says the state of a site's backups could not be read.
	BackupsUnknown
	// SiteUnavailable says the site's state could not be read, so the page shows less.
	SiteUnavailable
	// UpdatingSite says a site is being updated, in plain words (%s: site, %s: environment).
	UpdatingSite
	// SavingContent says a backup is copying the site's content, in plain words.
	SavingContent
	// PuttingContentBack says a restore is putting the backup's content back, in plain words.
	PuttingContentBack
	// ReadingSettings says the server is reading the latest settings, in plain words.
	ReadingSettings
	// AlreadyHasVersion says the site already has the version, so it is only updated again, in plain words.
	AlreadyHasVersion
	// NewVersionPublished says the new version is published, in plain words.
	NewVersionPublished
	// SiteUpdated says an update of the site finished, in plain words.
	SiteUpdated

	// keyCount is the number of messages; it stays last.
	keyCount
)

// english is the English catalogue.
//
//nolint:dupl,gochecknoglobals,lll // one fixed table of data, the same shape as persian, with sentences as long as they need
var english = [keyCount]string{
	SignOut:            "Sign out",
	LanguageLabel:      "Language",
	SitesTitle:         "Sites",
	SiteColumn:         "Site",
	NoSites:            "There are no sites for you to see.",
	NotInEnvironment:   "Not here",
	ReadyLabel:         "Ready",
	Unknown:            "unknown",
	NewerIn:            "Another version in %s",
	Loading:            "Loading…",
	CommitLabel:        "Commit",
	UncommittedChanges: "with uncommitted changes",
	DriftLabel:         "Drift",
	DriftNone:          "Matches Git",
	DriftChanged:       "Differs from Git",
	DriftNotInstalled:  "Not installed",
	DriftUnknown:       "Could not compare",
	LastBackup:         "Last backup",
	Never:              "Never",
	BackupsOff:         "Backups are off",
	AddressLabel:       "Address",
	VersionLabel:       "Version",
	ImageLabel:         "Image",
	PodsTitle:          "Pods",
	PodLabel:           "Pod",
	NodeLabel:          "Node",
	StateLabel:         "State",
	Yes:                "Yes",
	No:                 "No",
	NoPods:             "No pods are running.",
	ReleaseTitle:       "Release",
	RevisionLabel:      "Revision",
	UpdatedLabel:       "Updated",
	NotInstalled:       "This site is not installed in this environment.",
	VolumeLabel:        "Uploads volume",
	VolumeMissing:      "not found",
	HistoryTitle:       "History of the site's settings",
	AuthorLabel:        "Author",
	WhenLabel:          "When",
	ChangeLabel:        "Change",
	NoHistory:          "No changes yet.",
	HistoryUnavailable: "The history could not be read.",
	DiffTitle:          "What a deploy would change",
	NoDiff:             "Nothing; the cluster matches Git.",
	DiffUnavailable:    "The difference could not be computed: %s",
	BackupsTitle:       "Backups",
	TakenLabel:         "Taken",
	DatabaseLabel:      "Database",
	ChangedFilesLabel:  "Changed files",
	KeptUntilLabel:     "Kept until",
	NoBackups:          "No backups yet.",
	BackupsUnavailable: "The backups could not be listed.",
	OpenAdmin:          "Open admin",
	Publish:            "Publish %s to %s",
	DeployButton:       "Deploy from Git",
	BackUpNow:          "Back up now",
	RestoreButton:      "Restore this backup",
	RestoreUploads:     "Restore the uploaded files too",
	RestoreConfirm:     "This overwrites the site's database. Type %s to confirm.",
	RestoreMismatch:    "The name you typed is not the site's name, so nothing was restored.",
	ActionDeploy:       "Deploy",
	ActionPromote:      "Publish",
	ActionLogin:        "Admin login",
	ActionBackup:       "Backup",
	ActionRestore:      "Restore",
	Busy:               "%s is already running on %s in %s. Try again when it ends.",
	RunningNow:         "Running now: %s",
	RefusedTitle:       "Not allowed",
	Forbidden:          "You are not allowed to do this.",
	NoAccessTitle:      "No access",
	NoAccess:           "Your account has no access to any site. Ask the team that runs wpfleet to add one of your groups.",
	ExpiredForm:        "This form has expired. Go back, reload the page and try again.",
	NotFoundTitle:      "Not found",
	NotFound:           "There is no such page.",
	ErrorTitle:         "Something went wrong",
	ServerError:        "The server could not do this. Try again in a few minutes.",
	BadRequest:         "The request was not understood.",
	SignedOutTitle:     "Signed out",
	SignedOut:          "You have signed out.",
	SignIn:             "Sign in",
	SignInFailed:       "Signing in failed. Try again.",
	Back:               "Back to the site",
	TaskTitle:          "%s: %s in %s",
	Working:            "Working…",
	TaskDone:           "Done.",
	TaskFailed:         "The action failed.",
	FailedSync:         "Reading the latest settings from Git failed.",
	FailedPublish:      "Publishing the version failed; nothing was deployed.",
	FailedDeploy:       "Deploying the site failed.",
	FailedBackup:       "The backup failed.",
	FailedRestore:      "The restore failed.",
	FailedStopped:      "The server stopped before the action finished.",
	FailureDetail:      "Details: %s",
	Deploying:          "Deploying %s in %s…",
	JobStarted:         "Started the job %s.",
	Published:          "Published as commit %s.",
	AlreadyPublished:   "This environment already has that version; deploying it again.",
	Syncing:            "Reading the latest settings from Git…",
	Deployed:           "Deployed revision %s.",
	Restored:           "The backup is restored.",
	BackedUp:           "The backup is taken.",
	UnknownSite:        "There is no such site in this environment.",
	NoPromoteFrom:      "Nothing publishes into this environment.",
	SkipToContent:      "Skip to content",
	AccountMenu:        "Your account",
	NeedHelp:           "Need help?",
	PoweredBy:          "Powered by",
	LicenseName:        "Apache-2.0 license",
	SourceCode:         "Source code",
	SignInHeading:      "Welcome to %s",
	SignInIntro:        "Here you open your websites' dashboards, publish changes and take backups.",
	SignInButton:       "Sign in with your company account",
	SignInHint:         "Use the account you sign in to your other work tools with. You do not need a WordPress password.",
	SignInTrouble:      "Trouble signing in?",
	ContactSupport:     "Contact support",

	JustNow:             "just now",
	MinuteAgo:           "%s minute ago",
	MinutesAgo:          "%s minutes ago",
	HourAgo:             "%s hour ago",
	HoursAgo:            "%s hours ago",
	DayAgo:              "%s day ago",
	DaysAgo:             "%s days ago",
	TodayAt:             "Today at %s UTC",
	YesterdayAt:         "Yesterday at %s UTC",
	DateAt:              "%s at %s UTC",
	HomeTitle:           "Your sites",
	SiteCountOne:        "%s site",
	SiteCountMany:       "%s sites",
	SitesMatching:       "%s of %s sites match.",
	SearchLabel:         "Find a site",
	SearchHint:          "Type part of a site's name or address.",
	SearchButton:        "Search",
	ShowAllSites:        "Show all sites",
	NoMatch:             "No site matches “%s”.",
	ViewSite:            "View site",
	OpensInNewTab:       "%s (opens in a new tab)",
	StatusOnline:        "Online",
	StatusStarting:      "Starting",
	StatusDown:          "Down",
	StatusUnknown:       "Status unknown",
	LiveBadge:           "Live",
	BackedUpAgo:         "Backed up %s",
	NotBackedUpYet:      "Not backed up yet",
	NewerWaiting:        "A newer version is waiting in %s.",
	NoSitesYet:          "There are no sites for you here yet.",
	AskAdmin:            "Ask the person who set up this tool for your team.",
	OpenAdminOf:         "Open the admin of %s in %s",
	OpenAdminHelp:       "Sign in to the WordPress dashboard as yourself, without a password.",
	AdminBlocked:        "%s is running on this site. The dashboard opens again when it ends.",
	ViewSiteHelp:        "Open the site as visitors see it, in a new tab.",
	BackUpNowHelp:       "Save a copy of the site's content and uploaded files now.",
	RestoreBackup:       "Restore a backup",
	RestoreBackupHelp:   "Put the site's content back as it was in an earlier backup.",
	PublishHelp:         "Put the version that %s shows now on %s.",
	DeployHelp:          "Deploy the settings the repository holds now, without changing the version.",
	StatusLabel:         "Status",
	TechnicalDetails:    "Technical details",
	ReleasesTitle:       "Releases",
	NoReleases:          "No releases yet.",
	OtherEnvironments:   "This site elsewhere:",
	NoAddress:           "This site has no address yet.",
	DatabaseSize:        "content %s",
	KeptUntil:           "kept until %s",
	NotRestorable:       "no copy of the content; it cannot be restored",
	RestoreTitle:        "Restore a backup of %s",
	RestoreIntro:        "Restoring replaces the site's content and settings (posts, pages, users and plugin settings) with the copy in the backup you choose. Everything changed since that backup is lost.",
	RestoreWarning:      "This cannot be undone. If you may want today's content back, take a backup first.",
	ChooseBackup:        "Choose a backup",
	RestoreUploadsHelp:  "Images and documents changed or deleted since the backup are put back as they were; files uploaded after it stay.",
	RestoreConfirmLabel: "To confirm, type the site's name: %s",
	RestoreSubmit:       "Restore the site",
	NoRestorable:        "There is no backup to restore yet.",
	Cancel:              "Cancel",
	TaskHeadingDeploy:   "Deploying %s in %s",
	TaskHeadingPromote:  "Publishing %s to %s",
	TaskHeadingBackup:   "Backing up %s in %s",
	TaskHeadingRestore:  "Restoring %s in %s",
	TaskWait:            "This can take a few minutes. You may leave this page; the work goes on.",
	ProgressTitle:       "Progress",
	ShowLog:             "Show the technical log",
	NextSuccess:         "Check the site to see the result.",
	NextFailure:         "Try again in a few minutes. If it fails again, send the address of this page to your support team.",
	AskForAccess:        "If you need to do this, ask your support team for access.",
	GoHome:              "Back to your sites",
	BackupsUnknown:      "Could not check the backups right now",
	SiteUnavailable:     "The site's state could not be read right now. Try again in a minute.",
	UpdatingSite:        "Updating %s in %s…",
	SavingContent:       "Saving a copy of the content…",
	PuttingContentBack:  "Putting the content back…",
	ReadingSettings:     "Reading the latest settings…",
	AlreadyHasVersion:   "This site already has that version; updating it again.",
	NewVersionPublished: "The new version is published.",
	SiteUpdated:         "The site is updated.",
}

// persian is the Persian catalogue.
//
//nolint:dupl,gochecknoglobals,lll,staticcheck // as english; Persian spelling needs the zero-width non-joiner U+200C, which escapes would hide
var persian = [keyCount]string{
	SignOut:            "خروج",
	LanguageLabel:      "زبان",
	SitesTitle:         "سایت‌ها",
	SiteColumn:         "سایت",
	NoSites:            "سایتی برای نمایش به شما نیست.",
	NotInEnvironment:   "اینجا نیست",
	ReadyLabel:         "آماده",
	Unknown:            "نامعلوم",
	NewerIn:            "نسخهٔ دیگری در %s",
	Loading:            "در حال بارگذاری…",
	CommitLabel:        "کامیت",
	UncommittedChanges: "با تغییرهای کامیت‌نشده",
	DriftLabel:         "انحراف",
	DriftNone:          "هم‌خوان با Git",
	DriftChanged:       "متفاوت با Git",
	DriftNotInstalled:  "نصب نشده",
	DriftUnknown:       "مقایسه نشد",
	LastBackup:         "آخرین پشتیبان",
	Never:              "هرگز",
	BackupsOff:         "پشتیبان‌گیری خاموش است",
	AddressLabel:       "نشانی",
	VersionLabel:       "نسخه",
	ImageLabel:         "ایمیج",
	PodsTitle:          "پادها",
	PodLabel:           "پاد",
	NodeLabel:          "نود",
	StateLabel:         "وضعیت",
	Yes:                "بله",
	No:                 "خیر",
	NoPods:             "هیچ پادی در حال اجرا نیست.",
	ReleaseTitle:       "ریلیز",
	RevisionLabel:      "بازبینی",
	UpdatedLabel:       "به‌روزرسانی",
	NotInstalled:       "این سایت در این محیط نصب نشده است.",
	VolumeLabel:        "فضای فایل‌های بارگذاری‌شده",
	VolumeMissing:      "پیدا نشد",
	HistoryTitle:       "تاریخچهٔ تنظیمات سایت",
	AuthorLabel:        "نویسنده",
	WhenLabel:          "زمان",
	ChangeLabel:        "تغییر",
	NoHistory:          "هنوز تغییری نیست.",
	HistoryUnavailable: "تاریخچه خوانده نشد.",
	DiffTitle:          "آنچه استقرار تغییر می‌دهد",
	NoDiff:             "هیچ؛ کلاستر با Git هم‌خوان است.",
	DiffUnavailable:    "تفاوت محاسبه نشد: %s",
	BackupsTitle:       "پشتیبان‌ها",
	TakenLabel:         "زمان گرفتن",
	DatabaseLabel:      "پایگاه داده",
	ChangedFilesLabel:  "فایل‌های تغییرکرده",
	KeptUntilLabel:     "نگهداری تا",
	NoBackups:          "هنوز پشتیبانی نیست.",
	BackupsUnavailable: "فهرست پشتیبان‌ها خوانده نشد.",
	OpenAdmin:          "ورود به پیشخوان",
	Publish:            "انتشار %s در %s",
	DeployButton:       "استقرار از Git",
	BackUpNow:          "همین حالا پشتیبان بگیر",
	RestoreButton:      "بازگردانی این پشتیبان",
	RestoreUploads:     "فایل‌های بارگذاری‌شده هم بازگردانده شوند",
	RestoreConfirm:     "پایگاه دادهٔ سایت بازنویسی می‌شود. برای تأیید %s را بنویسید.",
	RestoreMismatch:    "نامی که نوشتید نام سایت نیست، پس چیزی بازگردانده نشد.",
	ActionDeploy:       "استقرار",
	ActionPromote:      "انتشار",
	ActionLogin:        "ورود به پیشخوان",
	ActionBackup:       "پشتیبان‌گیری",
	ActionRestore:      "بازگردانی",
	Busy:               "%s روی %s در %s در حال اجراست. پس از پایان آن دوباره تلاش کنید.",
	RunningNow:         "در حال اجرا: %s",
	RefusedTitle:       "مجاز نیست",
	Forbidden:          "شما اجازهٔ این کار را ندارید.",
	NoAccessTitle:      "بدون دسترسی",
	NoAccess:           "حساب شما به هیچ سایتی دسترسی ندارد. از تیمی که wpfleet را اداره می‌کند بخواهید یکی از گروه‌های شما را اضافه کند.",
	ExpiredForm:        "این فرم منقضی شده است. بازگردید، صفحه را دوباره بارگذاری کنید و دوباره تلاش کنید.",
	NotFoundTitle:      "پیدا نشد",
	NotFound:           "چنین صفحه‌ای نیست.",
	ErrorTitle:         "مشکلی پیش آمد",
	ServerError:        "سرور نتوانست این کار را انجام دهد. چند دقیقهٔ دیگر دوباره تلاش کنید.",
	BadRequest:         "درخواست فهمیده نشد.",
	SignedOutTitle:     "خارج شدید",
	SignedOut:          "از حساب خود خارج شدید.",
	SignIn:             "ورود",
	SignInFailed:       "ورود ناموفق بود. دوباره تلاش کنید.",
	Back:               "بازگشت به سایت",
	TaskTitle:          "%s: %s در %s",
	Working:            "در حال انجام…",
	TaskDone:           "انجام شد.",
	TaskFailed:         "این کار ناموفق بود.",
	FailedSync:         "خواندن آخرین تنظیمات از Git ناموفق بود.",
	FailedPublish:      "انتشار این نسخه ناموفق بود؛ چیزی مستقر نشد.",
	FailedDeploy:       "استقرار سایت ناموفق بود.",
	FailedBackup:       "پشتیبان‌گیری ناموفق بود.",
	FailedRestore:      "بازگردانی پشتیبان ناموفق بود.",
	FailedStopped:      "سرور پیش از پایان این کار متوقف شد.",
	FailureDetail:      "جزئیات: %s",
	Deploying:          "استقرار %s در %s…",
	JobStarted:         "کار %s شروع شد.",
	Published:          "با کامیت %s منتشر شد.",
	AlreadyPublished:   "این محیط همین نسخه را دارد؛ دوباره مستقر می‌شود.",
	Syncing:            "خواندن آخرین تنظیمات از Git…",
	Deployed:           "بازبینی %s مستقر شد.",
	Restored:           "پشتیبان بازگردانده شد.",
	BackedUp:           "پشتیبان گرفته شد.",
	UnknownSite:        "چنین سایتی در این محیط نیست.",
	NoPromoteFrom:      "هیچ محیطی در این محیط منتشر نمی‌شود.",
	SkipToContent:      "رفتن به محتوا",
	AccountMenu:        "حساب شما",
	NeedHelp:           "کمک لازم دارید؟",
	PoweredBy:          "ساخته‌شده با",
	LicenseName:        "مجوز Apache-2.0",
	SourceCode:         "کد منبع",
	SignInHeading:      "به %s خوش آمدید",
	SignInIntro:        "از اینجا داشبورد سایت‌هایتان را باز می‌کنید، تغییرها را منتشر می‌کنید و پشتیبان می‌گیرید.",
	SignInButton:       "ورود با حساب کاری شرکت",
	SignInHint:         "از همان حسابی استفاده کنید که با آن به دیگر ابزارهای کاری وارد می‌شوید. به رمز وردپرس نیازی ندارید.",
	SignInTrouble:      "در ورود مشکل دارید؟",
	ContactSupport:     "تماس با پشتیبانی",

	JustNow:             "همین حالا",
	MinuteAgo:           "%s دقیقه پیش",
	MinutesAgo:          "%s دقیقه پیش",
	HourAgo:             "%s ساعت پیش",
	HoursAgo:            "%s ساعت پیش",
	DayAgo:              "%s روز پیش",
	DaysAgo:             "%s روز پیش",
	TodayAt:             "امروز ساعت %s (UTC)",
	YesterdayAt:         "دیروز ساعت %s (UTC)",
	DateAt:              "%s ساعت %s (UTC)",
	HomeTitle:           "سایت‌های شما",
	SiteCountOne:        "%s سایت",
	SiteCountMany:       "%s سایت",
	SitesMatching:       "%s سایت از %s سایت پیدا شد.",
	SearchLabel:         "جست‌وجوی سایت",
	SearchHint:          "بخشی از نام یا نشانی سایت را بنویسید.",
	SearchButton:        "جست‌وجو",
	ShowAllSites:        "نمایش همهٔ سایت‌ها",
	NoMatch:             "هیچ سایتی با «%s» پیدا نشد.",
	ViewSite:            "دیدن سایت",
	OpensInNewTab:       "%s (در زبانهٔ تازه باز می‌شود)",
	StatusOnline:        "در دسترس",
	StatusStarting:      "در حال راه‌اندازی",
	StatusDown:          "از کار افتاده",
	StatusUnknown:       "وضعیت نامعلوم",
	LiveBadge:           "نسخهٔ اصلی",
	BackedUpAgo:         "پشتیبان‌گیری %s",
	NotBackedUpYet:      "هنوز پشتیبانی گرفته نشده",
	NewerWaiting:        "نسخهٔ تازه‌تری در %s آماده است.",
	NoSitesYet:          "هنوز سایتی برای شما اینجا نیست.",
	AskAdmin:            "از کسی که این ابزار را برای تیم شما راه انداخته است بپرسید.",
	OpenAdminOf:         "ورود به پیشخوان %s در %s",
	OpenAdminHelp:       "بدون گذرواژه، با حساب خودتان وارد پیشخوان وردپرس شوید.",
	AdminBlocked:        "%s روی این سایت در حال اجراست. پس از پایان آن، پیشخوان دوباره باز می‌شود.",
	ViewSiteHelp:        "سایت را همان‌طور که بازدیدکنندگان می‌بینند در زبانهٔ تازه باز کنید.",
	BackUpNowHelp:       "همین حالا از محتوا و فایل‌های بارگذاری‌شدهٔ سایت نسخهٔ پشتیبان بگیرید.",
	RestoreBackup:       "بازگردانی پشتیبان",
	RestoreBackupHelp:   "محتوای سایت را به حالت یکی از پشتیبان‌های پیشین برگردانید.",
	PublishHelp:         "نسخه‌ای که اکنون در %s است در %s هم نشان داده شود.",
	DeployHelp:          "تنظیمات کنونی مخزن بدون تغییر نسخه مستقر شود.",
	StatusLabel:         "وضعیت",
	TechnicalDetails:    "جزئیات فنی",
	ReleasesTitle:       "ریلیزها",
	NoReleases:          "هنوز ریلیزی نیست.",
	OtherEnvironments:   "این سایت در جاهای دیگر:",
	NoAddress:           "این سایت هنوز نشانی ندارد.",
	DatabaseSize:        "محتوا %s",
	KeptUntil:           "نگهداری تا %s",
	NotRestorable:       "نسخه‌ای از محتوا ندارد و بازگردانی نمی‌شود",
	RestoreTitle:        "بازگردانی پشتیبان %s",
	RestoreIntro:        "بازگردانی، محتوا و تنظیمات سایت (نوشته‌ها، برگه‌ها، کاربران و تنظیمات افزونه‌ها) را با نسخهٔ پشتیبانی که انتخاب می‌کنید جایگزین می‌کند. هر تغییری پس از آن پشتیبان از دست می‌رود.",
	RestoreWarning:      "این کار برگشت‌پذیر نیست. اگر ممکن است محتوای امروز را بخواهید، اول پشتیبان بگیرید.",
	ChooseBackup:        "یک پشتیبان انتخاب کنید",
	RestoreUploadsHelp:  "تصویرها و سندهایی که پس از پشتیبان تغییر کرده یا پاک شده‌اند به حالت پیشین برمی‌گردند؛ فایل‌هایی که پس از آن بارگذاری شده‌اند می‌مانند.",
	RestoreConfirmLabel: "برای تأیید، نام سایت را بنویسید: %s",
	RestoreSubmit:       "بازگردانی سایت",
	NoRestorable:        "هنوز پشتیبانی برای بازگردانی نیست.",
	Cancel:              "انصراف",
	TaskHeadingDeploy:   "استقرار %s در %s",
	TaskHeadingPromote:  "انتشار %s در %s",
	TaskHeadingBackup:   "پشتیبان‌گیری از %s در %s",
	TaskHeadingRestore:  "بازگردانی %s در %s",
	TaskWait:            "این کار ممکن است چند دقیقه طول بکشد. می‌توانید این صفحه را ببندید؛ کار ادامه پیدا می‌کند.",
	ProgressTitle:       "پیشرفت کار",
	ShowLog:             "نمایش گزارش فنی",
	NextSuccess:         "برای دیدن نتیجه به سایت سر بزنید.",
	NextFailure:         "چند دقیقهٔ دیگر دوباره تلاش کنید. اگر باز هم ناموفق بود، نشانی این صفحه را برای تیم پشتیبانی بفرستید.",
	AskForAccess:        "اگر به این کار نیاز دارید، از تیم پشتیبانی دسترسی بخواهید.",
	GoHome:              "بازگشت به سایت‌های شما",
	BackupsUnknown:      "وضعیت پشتیبان‌ها الان خوانده نشد",
	SiteUnavailable:     "وضعیت سایت الان خوانده نشد. یک دقیقهٔ دیگر دوباره امتحان کنید.",
	UpdatingSite:        "در حال به‌روزرسانی %s در %s…",
	SavingContent:       "در حال ذخیرهٔ یک نسخه از محتوا…",
	PuttingContentBack:  "در حال بازگرداندن محتوا…",
	ReadingSettings:     "در حال خواندن آخرین تنظیمات…",
	AlreadyHasVersion:   "این سایت همین نسخه را دارد؛ دوباره به‌روز می‌شود.",
	NewVersionPublished: "نسخهٔ تازه منتشر شد.",
	SiteUpdated:         "سایت به‌روز شد.",
}
