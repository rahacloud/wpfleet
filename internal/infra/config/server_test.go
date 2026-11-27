package config_test

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const serverSample = `environments:
  - name: development
    namespace: wp-development
  - name: staging
    namespace: wp-staging
    promoteFrom: development
server:
  listen: ":8080"
  metricsListen: ":9090"
  publicURL: https://wpfleet.example.com/
  git:
    url: git@git.example.com:example/fleet.git
    branch: main
    pollInterval: 30s
    committer: {name: wpfleet, email: wpfleet@example.com}
  oidc:
    issuer: https://sso.example.com/realms/main
    clientID: wpfleet
    scopes: [openid, email, groups]
  session:
    maxAge: 4h
  admin:
    matchEmail: false
  access:
    - group: platform
      environments: ["*"]
      sites: ["*"]
      allow: [view, deploy, login, backup, restore]
    - group: shop-team
      environments: [development, staging]
      sites: [shop]
      allow: [view, login]
`

func TestLoadServer(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(writeFleet(t, serverSample), noEnv)
	testutil.NoError(t, err)
	testutil.NoError(t, cfg.ValidateServer())

	s := cfg.Server
	testutil.Equal(t, "listen", s.Listen, ":8080")
	testutil.Equal(t, "public URL", s.PublicURL, "https://wpfleet.example.com/")
	testutil.Equal(t, "redirect", s.RedirectURL(), "https://wpfleet.example.com/auth/callback")
	testutil.Equal(t, "git url", s.Git.URL, "git@git.example.com:example/fleet.git")
	testutil.Equal(t, "poll", s.Git.PollInterval, 30*time.Second)
	testutil.Equal(t, "committer", s.Git.Committer, config.Committer{Name: "wpfleet", Email: "wpfleet@example.com"})
	testutil.Equal(t, "client", s.OIDC.ClientID, "wpfleet")
	testutil.DeepEqual(t, "scopes", s.OIDC.Scopes, []string{"openid", "email", "groups"})
	testutil.Equal(t, "max age", s.Session.MaxAge, 4*time.Hour)
	testutil.True(t, "match email off", !s.Admin.MatchEmail)

	// Keys the file leaves out keep their defaults.
	testutil.Equal(t, "groups claim", s.OIDC.GroupsClaim, "groups")

	testutil.DeepEqual(t, "access", s.Access, []config.AccessRule{
		{
			Group: "platform", Environments: []string{"*"}, Sites: []string{"*"},
			Allow: []string{"view", "deploy", "login", "backup", "restore"},
		},
		{
			Group: "shop-team", Environments: []string{"development", "staging"}, Sites: []string{"shop"},
			Allow: []string{"view", "login"},
		},
	})
}

func TestServerDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(writeFleet(t, sample), noEnv)
	testutil.NoError(t, err)

	s := cfg.Server
	testutil.Equal(t, "listen", s.Listen, ":8080")
	testutil.Equal(t, "metrics", s.MetricsListen, ":9090")
	testutil.Equal(t, "branch", s.Git.Branch, "main")
	testutil.Equal(t, "poll", s.Git.PollInterval, time.Minute)
	testutil.Equal(t, "committer name", s.Git.Committer.Name, "wpfleet")
	testutil.Equal(t, "groups claim", s.OIDC.GroupsClaim, "groups")
	testutil.Equal(t, "no scopes: the provider's defaults", len(s.OIDC.Scopes), 0)
	testutil.Equal(t, "max age", s.Session.MaxAge, 8*time.Hour)
	testutil.True(t, "match email", s.Admin.MatchEmail)
	testutil.Equal(t, "no rules", len(s.Access), 0)

	// The CLI loads a fleet without a server section; serving needs one.
	err = cfg.ValidateServer()
	testutil.ErrorIs(t, err, config.ErrInvalid)

	for _, key := range []string{
		"server.publicURL", "server.git.url", "server.oidc.issuer", "server.oidc.clientID", "committer",
	} {
		testutil.Contains(t, "missing "+key, err.Error(), key)
	}
}

func TestServerEnvironment(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(writeFleet(t, serverSample), func() []string {
		return []string{
			"WPFLEET_SERVER__LISTEN=:8081",
			"WPFLEET_SERVER__GIT__BRANCH=release",
			"WPFLEET_SERVER__PUBLICURL=https://other.example.com",
			"WPFLEET_SERVER__GIT__POLLINTERVAL=5m",
			"WPFLEET_SESSION_KEY=never-a-key",
			"WPFLEET_OIDC_CLIENT_SECRET=never-a-key",
			"WPFLEET_GIT_TOKEN=never-a-key",
			"WPFLEET_GIT_SSH_KEY=never-a-key",
			"WPFLEET_GIT_WEBHOOK_SECRET=never-a-key",
		}
	})
	testutil.NoError(t, err)
	testutil.Equal(t, "listen", cfg.Server.Listen, ":8081")
	testutil.Equal(t, "branch", cfg.Server.Git.Branch, "release")
	testutil.Equal(t, "camel-case key", cfg.Server.PublicURL, "https://other.example.com")
	testutil.Equal(t, "duration", cfg.Server.Git.PollInterval, 5*time.Minute)
	testutil.Equal(t, "file value kept", cfg.Server.OIDC.ClientID, "wpfleet")
}

func TestLoadFile(t *testing.T) {
	t.Parallel()

	dir := writeFleet(t, serverSample)
	path := filepath.Join(dir, config.FileName)
	moved := testutil.WriteFile(t, t.TempDir(), "server.yaml", testutil.ReadFile(t, path))

	cfg, err := config.LoadFile(moved, noEnv)
	testutil.NoError(t, err)
	testutil.Equal(t, "public URL", cfg.Server.PublicURL, "https://wpfleet.example.com/")

	_, err = config.LoadFile(filepath.Join(t.TempDir(), "missing.yaml"), noEnv)
	testutil.Error(t, err, "a missing file")
}

func TestValidateServerSection(t *testing.T) {
	t.Parallel()

	const envs = "environments:\n  - {name: staging, namespace: wp-staging}\n"

	tests := map[string]string{
		"pollInterval is negative": "server: {git: {pollInterval: -1m}}",
		"maxAge is negative":       "server: {session: {maxAge: -1h}}",
		"has no group":             `server: {access: [{environments: ["*"], sites: ["*"], allow: [view]}]}`,
		"names no environment":     `server: {access: [{group: g, sites: ["*"], allow: [view]}]}`,
		"unknown environment":      `server: {access: [{group: g, environments: [production], sites: ["*"], allow: [view]}]}`,
		"names no site":            `server: {access: [{group: g, environments: ["*"], allow: [view]}]}`,
		"names an empty site":      `server: {access: [{group: g, environments: ["*"], sites: [""], allow: [view]}]}`,
		"allows nothing":           `server: {access: [{group: g, environments: [staging], sites: [blog]}]}`,
	}

	for want, content := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(writeFleet(t, envs+content), noEnv)
			testutil.ErrorIs(t, err, config.ErrInvalid)
			testutil.Contains(t, "error", err.Error(), want)
		})
	}
}

func TestValidateServer(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*config.Server){
		"server.listen is empty":       func(s *config.Server) { s.Listen = "" },
		"off the public listener":      func(s *config.Server) { s.MetricsListen = s.Listen },
		"server.publicURL must be":     func(s *config.Server) { s.PublicURL = "wpfleet.example.com" },
		"server.oidc.issuer must be":   func(s *config.Server) { s.OIDC.Issuer = "ftp://sso.example.com" },
		"server.git.branch is empty":   func(s *config.Server) { s.Git.Branch = "" },
		"pollInterval must be":         func(s *config.Server) { s.Git.PollInterval = 0 },
		"maxAge must be positive":      func(s *config.Server) { s.Session.MaxAge = 0 },
		"groupsClaim is empty":         func(s *config.Server) { s.OIDC.GroupsClaim = "" },
		"server.publicURL: parse":      func(s *config.Server) { s.PublicURL = "https://[::1" },
		"committer needs a name":       func(s *config.Server) { s.Git.Committer.Email = "" },
		"server.oidc.clientID is empt": func(s *config.Server) { s.OIDC.ClientID = "" },
	}

	for want, change := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(writeFleet(t, serverSample), noEnv)
			testutil.NoError(t, err)

			change(&cfg.Server)

			err = cfg.ValidateServer()
			testutil.ErrorIs(t, err, config.ErrInvalid)
			testutil.Contains(t, "error", err.Error(), want)
		})
	}

	cfg, err := config.Load(writeFleet(t, serverSample), noEnv)
	testutil.NoError(t, err)

	cfg.Server.MetricsListen = ""
	testutil.NoError(t, cfg.ValidateServer())
}

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestServerSecretsFromEnv(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0xfb}, config.SessionKeySize)

	for name, encoded := range map[string]string{
		"standard":        base64.StdEncoding.EncodeToString(key),
		"raw standard":    base64.RawStdEncoding.EncodeToString(key),
		"url-safe":        base64.URLEncoding.EncodeToString(key),
		"raw url-safe":    base64.RawURLEncoding.EncodeToString(key),
		"trailing spaces": base64.StdEncoding.EncodeToString(key) + "\n",
	} {
		s, err := config.ServerSecretsFromEnv(env(map[string]string{
			config.EnvSessionKey:       encoded,
			config.EnvOIDCClientSecret: "client-secret",
			config.EnvGitSSHKey:        "/etc/wpfleet/git/ssh-privatekey",
			config.EnvGitWebhookSecret: "hook",
		}))
		testutil.NoError(t, err)
		testutil.True(t, name+" key", bytes.Equal(s.SessionKey, key))
		testutil.Equal(t, "client secret", s.OIDCClientSecret, "client-secret")
		testutil.Equal(t, "ssh key", s.GitSSHKeyFile, "/etc/wpfleet/git/ssh-privatekey")
		testutil.Equal(t, "webhook", string(s.GitWebhookSecret), "hook")
	}

	s, err := config.ServerSecretsFromEnv(env(map[string]string{
		config.EnvSessionKey: base64.StdEncoding.EncodeToString(key),
		config.EnvGitToken:   "token",
	}))
	testutil.NoError(t, err)
	testutil.Equal(t, "token", s.GitToken, "token")
	testutil.True(t, "no webhook secret", s.GitWebhookSecret == nil)

	for want, vars := range map[string]map[string]string{
		"is not set":    {},
		"decodes to 16": {config.EnvSessionKey: base64.StdEncoding.EncodeToString(key[:16])},
		"is not base64": {config.EnvSessionKey: "not base64!"},
		"choose one": {
			config.EnvSessionKey: base64.StdEncoding.EncodeToString(key), config.EnvGitSSHKey: "/k", config.EnvGitToken: "t",
		},
	} {
		_, err := config.ServerSecretsFromEnv(env(vars))
		testutil.ErrorIs(t, err, config.ErrInvalid)
		testutil.Contains(t, "error", err.Error(), want)
	}
}

func TestBrandingDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(writeFleet(t, sample), noEnv)
	testutil.NoError(t, err)

	b := cfg.Server.Branding
	testutil.Equal(t, "name", b.Name, "wpfleet")
	testutil.Equal(t, "color", b.PrimaryColor, "#1f5fbf")
	testutil.Equal(t, "no logo", b.Logo, "")
	testutil.Equal(t, "no favicon", b.Favicon, "")
	testutil.Equal(t, "no welcome", b.Welcome, config.Welcome{EN: "", FA: ""})
}

func TestLoadBranding(t *testing.T) {
	t.Parallel()

	const branding = `  branding:
    name: Northwind Media
    logo: brand/northwind.svg
    favicon: brand/favicon.png
    primaryColor: "#0E7C66"
    supportURL: https://help.example.com/wordpress
    supportEmail: web-help@example.com
    welcome:
      en: Open your site's dashboard from here.
      fa: داشبورد سایت خود را از اینجا باز کنید.
`

	cfg, err := config.Load(writeFleet(t, serverSample+branding), func() []string {
		return []string{"WPFLEET_SERVER__BRANDING__PRIMARYCOLOR=#123"}
	})
	testutil.NoError(t, err)
	testutil.NoError(t, cfg.ValidateServer())

	testutil.DeepEqual(t, "branding", cfg.Server.Branding, config.Branding{
		Name: "Northwind Media", Logo: "brand/northwind.svg", Favicon: "brand/favicon.png", PrimaryColor: "#123",
		SupportURL: "https://help.example.com/wordpress", SupportEmail: "web-help@example.com",
		Welcome: config.Welcome{EN: "Open your site's dashboard from here.", FA: "داشبورد سایت خود را از اینجا باز کنید."},
	})
}

func TestValidateBranding(t *testing.T) {
	t.Parallel()

	const envs = "environments:\n  - {name: staging, namespace: wp-staging}\n"

	long := strings.Repeat("a", config.MaxWelcomeLength+1)

	tests := map[string]string{
		"server.branding.name is empty":          `server: {branding: {name: " "}}`,
		"logo must be a relative path":           `server: {branding: {logo: /etc/passwd.png}}`,
		"inside the fleet repository, not \"../": `server: {branding: {logo: ../other/logo.svg}}`,
		"favicon must be a relative path":        `server: {branding: {favicon: 'brand\x5clogo.png'}}`,
		"logo must be a .png, .svg or .webp":     `server: {branding: {logo: brand/logo.gif}}`,
		"favicon must be a .png, .svg or .webp":  `server: {branding: {favicon: favicon.ico}}`,
		"primaryColor is not a CSS hex color":    `server: {branding: {primaryColor: blue}}`,
		"supportURL must be an absolute":         `server: {branding: {supportURL: help.example.com}}`,
		"supportEmail must be a plain address":   `server: {branding: {supportEmail: "Help <help@example.com>"}}`,
		"supportEmail must be a plain":           `server: {branding: {supportEmail: not-an-address}}`,
		"welcome.fa has 501 characters":          `server: {branding: {welcome: {fa: ` + long + `}}}`,
	}

	for want, content := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(writeFleet(t, envs+content), noEnv)
			testutil.ErrorIs(t, err, config.ErrInvalid)
			testutil.Contains(t, "error", err.Error(), want)
		})
	}
}
