package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rahacloud/wpfleet/internal/domain/brand"
)

// The environment variables that hold the server's secrets. They are never
// configuration keys, so no file can set them.
//
//nolint:gosec // G101: these are the names of variables, not credentials
const (
	// EnvOIDCClientSecret holds the OIDC client's secret.
	EnvOIDCClientSecret = "WPFLEET_OIDC_CLIENT_SECRET"
	// EnvSessionKey holds the session cookie's AES-256 key: 32 bytes in
	// base64.
	EnvSessionKey = "WPFLEET_SESSION_KEY"
	// EnvGitSSHKey holds the path of the Git deploy key file.
	EnvGitSSHKey = "WPFLEET_GIT_SSH_KEY"
	// EnvGitToken holds an HTTPS access token for the fleet repository.
	EnvGitToken = "WPFLEET_GIT_TOKEN"
	// EnvGitWebhookSecret holds the HMAC secret of the push webhook.
	EnvGitWebhookSecret = "WPFLEET_GIT_WEBHOOK_SECRET"
)

// SessionKeySize is the length of the session key: AES-256.
const SessionKeySize = 32

// CallbackPath is where the OIDC provider sends the browser back, under the
// public URL.
const CallbackPath = "/auth/callback"

// AnyName in an access rule's environments or sites matches every name.
const AnyName = "*"

// Server is the server section of wpfleet.yaml, read by wpfleet serve.
type Server struct {
	// Listen is the address of the UI and the webhook.
	Listen string `json:"listen" koanf:"listen"`
	// MetricsListen is the address of /metrics, kept off the public
	// listener; empty turns it off.
	MetricsListen string `json:"metricsListen" koanf:"metricsListen"`
	// PublicURL is the URL people open; the OIDC redirect is
	// <PublicURL>/auth/callback.
	PublicURL string `json:"publicURL" koanf:"publicURL"`
	// Git is the fleet repository the server clones.
	Git ServerGit `json:"git" koanf:"git"`
	// OIDC is the single sign-on provider.
	OIDC OIDC `json:"oidc" koanf:"oidc"`
	// Session configures the session cookie.
	Session Session `json:"session" koanf:"session"`
	// Admin configures the one-time admin login.
	Admin Admin `json:"admin" koanf:"admin"`
	// Access is the list of rules; whatever no rule allows is denied.
	Access []AccessRule `json:"access" koanf:"access"`
	// Branding is the fleet's own name, logo and color in the UI.
	Branding Branding `json:"branding" koanf:"branding"`
}

// Branding is how the UI presents itself: the client's name, logo, favicon
// and color, where to ask for help, and a welcome on the home page. The
// footer, which names wpfleet, its license and its repository, is not part
// of it and cannot be changed.
type Branding struct {
	// Name is shown beside the logo and in the browser's title.
	Name string `json:"name" koanf:"name"`
	// Logo is a PNG, SVG or WebP file of at most 512 KiB, as a path inside
	// the fleet repository; empty shows the name alone.
	Logo string `json:"logo" koanf:"logo"`
	// Favicon follows the logo's rules; empty uses wpfleet's own.
	Favicon string `json:"favicon" koanf:"favicon"`
	// PrimaryColor is a CSS hex color for buttons and links; the text on it
	// and its shades for each theme are derived so they stay readable.
	PrimaryColor string `json:"primaryColor" koanf:"primaryColor"`
	// SupportURL is where "Need help?" leads; optional.
	SupportURL string `json:"supportURL" koanf:"supportURL"`
	// SupportEmail is the address "Need help?" writes to when there is no
	// SupportURL; optional.
	SupportEmail string `json:"supportEmail" koanf:"supportEmail"`
	// Welcome is a short text on the home page, per language; optional.
	Welcome Welcome `json:"welcome" koanf:"welcome"`
}

// Welcome is a text in each language of the UI.
type Welcome struct {
	// EN is the English text.
	EN string `json:"en" koanf:"en"`
	// FA is the Persian text.
	FA string `json:"fa" koanf:"fa"`
}

// MaxWelcomeLength is the longest welcome text, in characters: a sentence
// or two, not a page.
const MaxWelcomeLength = 500

// ServerGit is the fleet repository the server clones and pushes to. Its
// credentials come from EnvGitSSHKey or EnvGitToken.
type ServerGit struct {
	// URL is the repository's SSH or HTTPS URL.
	URL string `json:"url" koanf:"url"`
	// Branch is the branch the server follows and pushes to.
	Branch string `json:"branch" koanf:"branch"`
	// PollInterval is how often the branch is fetched besides webhooks.
	PollInterval time.Duration `json:"pollInterval" koanf:"pollInterval"`
	// Committer is the server's bot identity in the commits it makes; the
	// author is the signed-in person.
	Committer Committer `json:"committer" koanf:"committer"`
}

// Committer is a Git identity.
type Committer struct {
	// Name is the display name.
	Name string `json:"name" koanf:"name"`
	// Email is the email address.
	Email string `json:"email" koanf:"email"`
}

// OIDC is an OpenID Connect provider that publishes discovery. The client
// secret comes from EnvOIDCClientSecret.
type OIDC struct {
	// Issuer is the issuer URL; discovery is under it.
	Issuer string `json:"issuer" koanf:"issuer"`
	// ClientID is the client registered for wpfleet.
	ClientID string `json:"clientID" koanf:"clientID"`
	// GroupsClaim is the ID token claim that lists the person's groups.
	GroupsClaim string `json:"groupsClaim" koanf:"groupsClaim"`
	// Scopes are the scopes the sign-in asks for. Empty asks for openid,
	// email and profile, and groups when the provider's discovery lists it
	// (Dex puts the groups claim in the ID token only then).
	Scopes []string `json:"scopes" koanf:"scopes"`
	// AssumeEmailVerified keeps an email claim that comes without
	// email_verified, for a provider whose email people cannot change. Off
	// by default: such an email is dropped and never matches a WordPress
	// user.
	AssumeEmailVerified bool `json:"assumeEmailVerified" koanf:"assumeEmailVerified"`
}

// Session configures the session cookie. Its key comes from EnvSessionKey.
type Session struct {
	// MaxAge is how long a sign-in lasts; the groups are read again at the
	// next one.
	MaxAge time.Duration `json:"maxAge" koanf:"maxAge"`
}

// Admin configures the one-time admin login.
type Admin struct {
	// MatchEmail logs in as the WordPress administrator whose email is the
	// signed-in person's, when there is one.
	MatchEmail bool `json:"matchEmail" koanf:"matchEmail"`
}

// AccessRule allows a group some permissions on some sites of some
// environments. It mirrors the access package's rule with plain strings;
// the permission names are checked when the server turns the rules into a
// policy.
type AccessRule struct {
	// Group is the group, as the groups claim names it.
	Group string `json:"group" koanf:"group"`
	// Environments are environment names, or "*" for all.
	Environments []string `json:"environments" koanf:"environments"`
	// Sites are site names, or "*" for all.
	Sites []string `json:"sites" koanf:"sites"`
	// Allow are the permissions: view, deploy, login, backup, restore.
	Allow []string `json:"allow" koanf:"allow"`
}

// DefaultServer is the server section before the file applies.
func DefaultServer() Server {
	return Server{
		Listen:        ":8080",
		MetricsListen: ":9090",
		PublicURL:     "",
		Git: ServerGit{
			URL:          "",
			Branch:       "main",
			PollInterval: time.Minute,
			Committer:    Committer{Name: "wpfleet", Email: ""},
		},
		OIDC:    OIDC{Issuer: "", ClientID: "", GroupsClaim: "groups", Scopes: nil, AssumeEmailVerified: false},
		Session: Session{MaxAge: 8 * time.Hour}, //nolint:mnd // the design's default sign-in length
		Admin:   Admin{MatchEmail: true},
		Access:  nil,
		Branding: Branding{
			Name: brand.DefaultName, Logo: "", Favicon: "", PrimaryColor: brand.DefaultPrimaryColor,
			SupportURL: "", SupportEmail: "", Welcome: Welcome{EN: "", FA: ""},
		},
	}
}

// RedirectURL is the OIDC redirect URL: <publicURL>/auth/callback.
func (s Server) RedirectURL() string {
	return strings.TrimSuffix(s.PublicURL, "/") + CallbackPath
}

// validate checks what is wrong in any server section, even one the CLI
// never uses: a negative duration or an access rule that cannot match.
// envs are the fleet's environment names.
func (s Server) validate(envs map[string]bool) []error {
	var errs []error

	if s.Git.PollInterval < 0 {
		errs = append(errs, fmt.Errorf("%w: server.git.pollInterval is negative", ErrInvalid))
	}

	if s.Session.MaxAge < 0 {
		errs = append(errs, fmt.Errorf("%w: server.session.maxAge is negative", ErrInvalid))
	}

	for i, r := range s.Access {
		errs = append(errs, r.validate(i, envs)...)
	}

	return append(errs, s.Branding.validate()...)
}

// validate checks the branding: a name, image paths that stay inside the
// fleet repository and name an allowed type, a hex color, an absolute
// support URL, a plain email address and short welcome texts. That the
// image files exist and match their type is checked by serve, which has
// the repository.
func (b Branding) validate() []error {
	var errs []error

	if strings.TrimSpace(b.Name) == "" {
		errs = append(errs, fmt.Errorf("%w: server.branding.name is empty (leave it out for %q)", ErrInvalid,
			brand.DefaultName))
	}

	errs = append(errs, checkBrandImage("server.branding.logo", b.Logo)...)
	errs = append(errs, checkBrandImage("server.branding.favicon", b.Favicon)...)

	_, err := brand.ParseColor(b.PrimaryColor)
	if err != nil {
		errs = append(errs, fmt.Errorf("%w: server.branding.primaryColor is %w", ErrInvalid, err))
	}

	if b.SupportURL != "" {
		errs = append(errs, checkURL("server.branding.supportURL", b.SupportURL))
	}

	if b.SupportEmail != "" {
		a, err := mail.ParseAddress(b.SupportEmail)
		if err != nil || a.Name != "" || a.Address != b.SupportEmail {
			errs = append(errs, fmt.Errorf("%w: server.branding.supportEmail must be a plain address like "+
				"help@example.com, not %q", ErrInvalid, b.SupportEmail))
		}
	}

	errs = append(errs, checkWelcome("en", b.Welcome.EN), checkWelcome("fa", b.Welcome.FA))

	return errs
}

// checkBrandImage checks a logo or favicon path: empty, or relative inside
// the fleet repository with an allowed extension.
func checkBrandImage(key, p string) []error {
	if p == "" {
		return nil
	}

	var errs []error

	if !filepath.IsLocal(p) || strings.Contains(p, `\`) {
		errs = append(errs, fmt.Errorf("%w: %s must be a relative path inside the fleet repository, not %q",
			ErrInvalid, key, p))
	}

	_, err := brand.TypeOf(p)
	if err != nil {
		errs = append(errs, fmt.Errorf("%w: %s %w", ErrInvalid, key, err))
	}

	return errs
}

// checkWelcome checks that a welcome text is short.
func checkWelcome(lang, text string) error {
	if n := utf8.RuneCountInString(text); n > MaxWelcomeLength {
		return fmt.Errorf("%w: server.branding.welcome.%s has %d characters, more than %d",
			ErrInvalid, lang, n, MaxWelcomeLength)
	}

	return nil
}

// validate checks one access rule.
func (r AccessRule) validate(i int, envs map[string]bool) []error {
	var errs []error

	where := fmt.Sprintf("server.access[%d]", i)

	if r.Group == "" {
		errs = append(errs, fmt.Errorf("%w: %s has no group", ErrInvalid, where))
	}

	if len(r.Environments) == 0 {
		errs = append(errs, fmt.Errorf("%w: %s names no environment (use \"*\" for all)", ErrInvalid, where))
	}

	for _, e := range r.Environments {
		if e != AnyName && !envs[e] {
			errs = append(errs, fmt.Errorf("%w: %s names unknown environment %q", ErrInvalid, where, e))
		}
	}

	if len(r.Sites) == 0 {
		errs = append(errs, fmt.Errorf("%w: %s names no site (use \"*\" for all)", ErrInvalid, where))
	}

	for _, site := range r.Sites {
		if site == "" {
			errs = append(errs, fmt.Errorf("%w: %s names an empty site", ErrInvalid, where))
		}
	}

	if len(r.Allow) == 0 {
		errs = append(errs, fmt.Errorf("%w: %s allows nothing", ErrInvalid, where))
	}

	return errs
}

// ValidateServer checks that the server section has what wpfleet serve
// needs: a listen address, an absolute public URL, the repository, the
// committer, the OIDC provider and a positive poll interval and session
// length. Load does not call it, because the CLI needs none of it.
func (c Config) ValidateServer() error {
	s := c.Server

	var errs []error

	if s.Listen == "" {
		errs = append(errs, fmt.Errorf("%w: server.listen is empty", ErrInvalid))
	}

	if s.MetricsListen != "" && s.MetricsListen == s.Listen {
		errs = append(errs, fmt.Errorf(
			"%w: server.metricsListen is server.listen; /metrics stays off the public listener", ErrInvalid))
	}

	errs = append(errs, checkURL("server.publicURL", s.PublicURL))
	errs = append(errs, s.Git.validate()...)
	errs = append(errs, s.OIDC.validate()...)

	if s.Session.MaxAge <= 0 {
		errs = append(errs, fmt.Errorf("%w: server.session.maxAge must be positive", ErrInvalid))
	}

	return errors.Join(errs...)
}

// validate checks the repository settings serve needs.
func (g ServerGit) validate() []error {
	var errs []error

	if g.URL == "" {
		errs = append(errs, fmt.Errorf("%w: server.git.url is empty", ErrInvalid))
	}

	if g.Branch == "" {
		errs = append(errs, fmt.Errorf("%w: server.git.branch is empty", ErrInvalid))
	}

	if g.PollInterval <= 0 {
		errs = append(errs, fmt.Errorf("%w: server.git.pollInterval must be positive", ErrInvalid))
	}

	if g.Committer.Name == "" || g.Committer.Email == "" {
		errs = append(errs, fmt.Errorf("%w: server.git.committer needs a name and an email", ErrInvalid))
	}

	return errs
}

// validate checks the provider settings serve needs.
func (o OIDC) validate() []error {
	errs := []error{checkURL("server.oidc.issuer", o.Issuer)}

	if o.ClientID == "" {
		errs = append(errs, fmt.Errorf("%w: server.oidc.clientID is empty", ErrInvalid))
	}

	if o.GroupsClaim == "" {
		errs = append(errs, fmt.Errorf("%w: server.oidc.groupsClaim is empty", ErrInvalid))
	}

	return errs
}

// checkURL requires an absolute http or https URL with a host.
func checkURL(key, raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalid, key)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalid, key, err)
	}

	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: %s must be an absolute http or https URL, not %q", ErrInvalid, key, raw)
	}

	return nil
}

// ServerSecrets are the server's secrets, read only from the environment.
type ServerSecrets struct {
	// OIDCClientSecret is the OIDC client's secret; empty for a public
	// client, which PKCE still protects.
	OIDCClientSecret string
	// SessionKey is the AES-256 key of the session cookie.
	SessionKey []byte
	// GitSSHKeyFile is the deploy key's path, for an SSH repository URL.
	GitSSHKeyFile string
	// GitToken is the access token, for an HTTPS repository URL.
	GitToken string
	// GitWebhookSecret signs the push webhook; empty refuses every webhook.
	GitWebhookSecret []byte
}

// ServerSecretsFromEnv reads the server's secrets from getenv (os.Getenv
// in the server). The session key is required and must decode from base64
// to SessionKeySize bytes; a deploy key and a token together are refused.
func ServerSecretsFromEnv(getenv func(string) string) (ServerSecrets, error) {
	s := ServerSecrets{
		OIDCClientSecret: getenv(EnvOIDCClientSecret),
		SessionKey:       nil,
		GitSSHKeyFile:    getenv(EnvGitSSHKey),
		GitToken:         getenv(EnvGitToken),
		GitWebhookSecret: []byte(getenv(EnvGitWebhookSecret)),
	}

	var errs []error

	key, err := decodeKey(getenv(EnvSessionKey))
	if err != nil {
		errs = append(errs, err)
	}

	s.SessionKey = key

	if s.GitSSHKeyFile != "" && s.GitToken != "" {
		errs = append(errs, fmt.Errorf("%w: both %s and %s are set; choose one", ErrInvalid, EnvGitSSHKey, EnvGitToken))
	}

	if len(s.GitWebhookSecret) == 0 {
		s.GitWebhookSecret = nil
	}

	return s, errors.Join(errs...)
}

// decodeKey decodes the session key from standard or URL-safe base64,
// padded or not.
func decodeKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: %s is not set (32 random bytes in base64, e.g. openssl rand -base64 32)",
			ErrInvalid, EnvSessionKey)
	}

	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		key, err := enc.DecodeString(raw)
		if err != nil {
			continue
		}

		if len(key) != SessionKeySize {
			return nil, fmt.Errorf("%w: %s decodes to %d bytes, not %d", ErrInvalid, EnvSessionKey, len(key), SessionKeySize)
		}

		return key, nil
	}

	return nil, fmt.Errorf("%w: %s is not base64", ErrInvalid, EnvSessionKey)
}
