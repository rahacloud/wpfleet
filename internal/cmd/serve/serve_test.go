package serve_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/cmd/serve"
	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/infra/oidc"
	"github.com/rahacloud/wpfleet/internal/infra/oidc/oidctest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	clientID      = "wpfleet"
	clientSecret  = "client-secret"
	webhookSecret = "hook-secret"
)

// serverConfig is a server's wpfleet.yaml for a fleet at gitURL and a
// provider at issuer, with the access rules given.
func serverConfig(gitURL, issuer, access string) string {
	return `environments:
  - name: staging
    namespace: wp-staging
server:
  listen: 127.0.0.1:0
  metricsListen: localhost:0
  publicURL: http://wpfleet.example.com
  git:
    url: ` + gitURL + `
    branch: main
    pollInterval: 1h
    committer: {name: wpfleet, email: wpfleet@example.com}
  oidc:
    issuer: ` + issuer + `
    clientID: ` + clientID + `
  access:
` + access
}

const platformRule = `    - group: platform
      environments: ["*"]
      sites: ["*"]
      allow: [view, deploy, login, backup, restore]
`

func secrets(extra map[string]string) func(string) string {
	env := map[string]string{
		config.EnvSessionKey:       base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", config.SessionKeySize))),
		config.EnvOIDCClientSecret: clientSecret,
		config.EnvGitWebhookSecret: webhookSecret,
	}

	maps.Copy(env, extra)

	return func(k string) string { return env[k] }
}

// started is a running server and how to reach and stop it.
type started struct {
	base, metrics string
	client        *http.Client
	stop          func() error
}

func start(t *testing.T, configFile string, getenv func(string) string) started {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	addrs := make(chan [2]net.Addr, 1)
	done := make(chan error, 1)

	go func() {
		done <- serve.Run(ctx, slog.New(slog.DiscardHandler), configFile, t.TempDir(), getenv,
			func(listen, metrics net.Addr) { addrs <- [2]net.Addr{listen, metrics} })
	}()

	var a [2]net.Addr

	select {
	case a = <-addrs:
	case err := <-done:
		cancel()
		t.Fatalf("the server did not start: %v", err)
	}

	jar, err := cookiejar.New(nil)
	testutil.NoError(t, err)

	//nolint:exhaustruct_v5 // a test client that stops at redirects
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       10 * time.Second,
	}

	return started{
		base: "http://" + a[0].String(), metrics: "http://" + a[1].String(), client: client,
		stop: func() error {
			cancel()

			return <-done
		},
	}
}

// response is what a request got back.
type response struct {
	status int
	header http.Header
	body   string
}

func (s started) do(t *testing.T, method, url string, body io.Reader, header http.Header) response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	testutil.NoError(t, err)

	maps.Copy(req.Header, header)

	resp, err := s.client.Do(req)
	testutil.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	testutil.NoError(t, err)

	return response{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

func TestServeSignsInAndServesTheFleet(t *testing.T) {
	t.Parallel()

	provider := oidctest.New(t, clientID, clientSecret)
	provider.SignInAs(oidctest.Claims{
		"sub": "u1", "email": "admin@example.com", "email_verified": true, "name": "An Admin",
		"groups": []string{groupPlatform, "other"},
	})

	gitURL := remote(t, fleetFiles())
	configFile := testutil.WriteFile(t, t.TempDir(), "wpfleet.yaml", serverConfig(gitURL, provider.Issuer(), platformRule))
	s := start(t, configFile, secrets(nil))

	resp := s.do(t, http.MethodGet, s.base+"/healthz", nil, nil)
	testutil.Equal(t, "healthz", resp.status, http.StatusOK)

	resp = s.do(t, http.MethodGet, s.base+"/readyz", nil, nil)
	testutil.Equal(t, "readyz", resp.status, http.StatusOK)

	resp = s.do(t, http.MethodGet, s.metrics+"/metrics", nil, nil)
	testutil.Equal(t, "metrics", resp.status, http.StatusOK)
	testutil.Contains(t, "metrics", resp.body, "go_goroutines")

	resp = s.do(t, http.MethodGet, s.base+"/metrics", nil, nil)
	testutil.True(t, "no metrics on the UI listener", resp.status != http.StatusOK)

	resp = s.do(t, http.MethodGet, s.base+"/", nil, nil)
	testutil.Equal(t, "sent to sign in", resp.status, http.StatusSeeOther)

	resp = s.do(t, http.MethodGet, s.base+"/auth/login?return=/", nil, nil)
	testutil.Equal(t, "login redirects", resp.status, http.StatusSeeOther)

	authURL := resp.header.Get("Location")
	testutil.Contains(t, "to the provider", authURL, provider.Issuer(), "code_challenge_method=S256",
		url.QueryEscape("http://wpfleet.example.com/auth/callback"))

	code, state := provider.Authorize(t, authURL)

	callback := s.base + "/auth/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	resp = s.do(t, http.MethodGet, callback, nil, nil)
	testutil.Equal(t, "callback", resp.status, http.StatusSeeOther)
	testutil.Equal(t, "back where the person was", resp.header.Get("Location"), "/")

	resp = s.do(t, http.MethodGet, s.base+"/", nil, nil)
	testutil.Equal(t, "fleet page", resp.status, http.StatusOK)
	testutil.Contains(t, "the fleet's site", resp.body, "blog", "admin@example.com")

	testutil.NoError(t, s.stop())
}

func TestServeWebhookSyncs(t *testing.T) {
	t.Parallel()

	provider := oidctest.New(t, clientID, clientSecret)
	gitURL := remote(t, fleetFiles())
	configFile := testutil.WriteFile(t, t.TempDir(), "wpfleet.yaml", serverConfig(gitURL, provider.Issuer(), platformRule))
	s := start(t, configFile, secrets(nil))

	body := `{"ref":"refs/heads/main"}`

	resp := s.do(t, http.MethodPost, s.base+"/hooks/git", strings.NewReader(body),
		http.Header{"X-Hub-Signature-256": {"sha256=" + strings.Repeat("0", 64)}})
	testutil.Equal(t, "wrong signature", resp.status, http.StatusUnauthorized)

	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write([]byte(body))

	resp = s.do(t, http.MethodPost, s.base+"/hooks/git", strings.NewReader(body),
		http.Header{"X-Hub-Signature-256": {"sha256=" + hex.EncodeToString(mac.Sum(nil))}})
	testutil.Equal(t, "signed webhook", resp.status, http.StatusAccepted)

	testutil.NoError(t, s.stop())
}

func TestServeRefusesToStart(t *testing.T) {
	t.Parallel()

	provider := oidctest.New(t, clientID, clientSecret)
	gitURL := remote(t, fleetFiles())

	cases := map[string]struct {
		config string
		getenv func(string) string
		want   string
	}{
		"no access rule": {
			config: serverConfig(gitURL, provider.Issuer(), "    []\n"),
			getenv: secrets(nil), want: "server.access has no rule",
		},
		"no session key": {
			config: serverConfig(gitURL, provider.Issuer(), platformRule),
			getenv: secrets(map[string]string{config.EnvSessionKey: ""}), want: config.EnvSessionKey,
		},
		"no OIDC issuer": {
			config: serverConfig(gitURL, "", platformRule),
			getenv: secrets(nil), want: "server.oidc.issuer",
		},
		"unknown permission": {
			config: serverConfig(gitURL, provider.Issuer(),
				"    - group: platform\n      environments: [\"*\"]\n      sites: [\"*\"]\n      allow: [everything]\n"),
			getenv: secrets(nil), want: "everything",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			configFile := testutil.WriteFile(t, t.TempDir(), "wpfleet.yaml", tc.config)

			err := serve.Run(t.Context(), slog.New(slog.DiscardHandler), configFile, t.TempDir(), tc.getenv, nil)
			testutil.Error(t, err, name)
			testutil.Contains(t, "error", err.Error(), tc.want)
		})
	}
}

func TestAccessPolicyConverts(t *testing.T) {
	t.Parallel()

	_, err := serve.AccessPolicy(nil)
	testutil.ErrorIs(t, err, serve.ErrNoAccess)

	policy, err := serve.AccessPolicy([]config.AccessRule{
		{Group: groupShop, Environments: []string{envStaging}, Sites: []string{"shop"}, Allow: []string{"login"}},
	})
	testutil.NoError(t, err)
	testutil.True(t, "login allowed", policy.Allows([]string{groupShop}, "staging", "shop", access.Login))
	testutil.True(t, "deploy denied", !policy.Allows([]string{groupShop}, "staging", "shop", access.Deploy))

	_, err = serve.AccessPolicy([]config.AccessRule{
		{Group: groupShop, Environments: []string{envStaging}, Sites: []string{"shop"}, Allow: []string{"root"}},
	})
	testutil.ErrorIs(t, err, access.ErrInvalidRule)
}

func TestCheckFleetRefusesAnUnknownEnvironment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, content := range fleetFiles() {
		testutil.WriteFile(t, dir, name, content)
	}

	rule := access.Rule{
		Group: "g", Environments: []string{"production"}, Sites: []string{"*"}, Allow: []access.Permission{access.View},
	}
	err := serve.CheckFleet(dir, access.Policy{Rules: []access.Rule{rule}})
	testutil.Error(t, err, "unknown environment")
	testutil.Contains(t, "error", err.Error(), `"production"`)

	rule.Environments = []string{envStaging}
	testutil.NoError(t, serve.CheckFleet(dir, access.Policy{Rules: []access.Rule{rule}}))
	testutil.Error(t, serve.CheckFleet(filepath.Join(dir, "missing"), access.Policy{Rules: nil}), "no fleet")
}

func TestOnlyRuleGroupsReachTheSession(t *testing.T) {
	t.Parallel()

	server := oidctest.New(t, clientID, clientSecret)
	server.SignInAs(oidctest.Claims{"sub": "u1", "email": "a@example.com", "groups": []string{"x", groupPlatform, "y"}})

	p, err := oidc.New(t.Context(), oidc.Config{
		Issuer: server.Issuer(), ClientID: clientID, ClientSecret: clientSecret,
		RedirectURL: "http://wpfleet.example.com/auth/callback", GroupsClaim: "", Scopes: nil,
		AssumeEmailVerified: false,
	})
	testutil.NoError(t, err)

	verifier := strings.Repeat("v", 43)
	code, _ := server.Authorize(t, p.AuthURL("state", "nonce", verifier))

	policy := access.Policy{Rules: []access.Rule{
		{Group: groupPlatform, Environments: []string{"*"}, Sites: []string{"*"}, Allow: []access.Permission{access.View}},
	}}

	id, err := serve.FilterGroups(t.Context(), p, policy, code, verifier, "nonce")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "groups", id.Groups, []string{groupPlatform})
}
