package oidc_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"strings"
	"testing"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/oidc"
	"github.com/rahacloud/wpfleet/internal/infra/oidc/oidctest"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	clientID     = "wpfleet"
	clientSecret = "s3cret"
	redirectURL  = "https://wpfleet.example.com/auth/callback"

	claimSub      = "sub"
	claimGroups   = "groups"
	claimRoles    = "roles"
	claimEmail    = "email"
	editorEmail   = "editor@example.com"
	anotherClient = "another-client"
	platform      = "platform"
)

func config(issuer string) oidc.Config {
	return oidc.Config{
		Issuer:       issuer,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		GroupsClaim:  "",
		Scopes:       nil,

		AssumeEmailVerified: false,
	}
}

// setup starts a provider and discovers it.
func setup(t *testing.T, change ...func(*oidc.Config)) (*oidctest.Server, *oidc.Provider) {
	t.Helper()

	server := oidctest.New(t, clientID, clientSecret)
	c := config(server.Issuer())

	for _, f := range change {
		f(&c)
	}

	provider, err := oidc.New(t.Context(), c)
	testutil.NoError(t, err)

	return server, provider
}

// flow is one sign-in's secrets.
type flow struct{ state, nonce, verifier string }

func newFlow(t *testing.T) flow {
	t.Helper()

	f, err := session.NewFlow("/")
	testutil.NoError(t, err)

	return flow{state: f.State, nonce: f.Nonce, verifier: f.Verifier}
}

// signIn runs the whole flow and returns the identity or the error.
func signIn(t *testing.T, server *oidctest.Server, provider *oidc.Provider) (access.Identity, error) {
	t.Helper()

	f := newFlow(t)

	code, state := server.Authorize(t, provider.AuthURL(f.state, f.nonce, f.verifier))
	testutil.Equal(t, "state", state, f.state)

	return provider.Exchange(t.Context(), code, f.verifier, f.nonce)
}

func TestSignIn(t *testing.T) {
	t.Parallel()

	server, provider := setup(t)
	server.SignInAs(oidctest.Claims{
		claimSub:         "user-42",
		claimEmail:       editorEmail,
		"email_verified": true,
		"name":           "Example Editor",
		claimGroups:      []string{"shop-team", "staff"},
		"phone_number":   "+10000000000",
	})

	id, err := signIn(t, server, provider)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "identity", id, access.Identity{
		Subject: "user-42",
		Email:   editorEmail,
		Name:    "Example Editor",
		Groups:  []string{"shop-team", "staff"},
	})
}

func TestAuthURL(t *testing.T) {
	t.Parallel()

	server, provider := setup(t)
	f := newFlow(t)

	u, err := url.Parse(provider.AuthURL(f.state, f.nonce, f.verifier))
	testutil.NoError(t, err)

	q := u.Query()
	challenge := sha256.Sum256([]byte(f.verifier))

	testutil.True(t, "authorization endpoint", strings.HasPrefix(u.String(), server.Issuer()+"/authorize?"))
	testutil.Equal(t, "response_type", q.Get("response_type"), "code")
	testutil.Equal(t, "client_id", q.Get("client_id"), clientID)
	testutil.Equal(t, "redirect_uri", q.Get("redirect_uri"), redirectURL)
	testutil.Equal(t, "scope", q.Get("scope"), "openid email profile")
	testutil.Equal(t, "state", q.Get("state"), f.state)
	testutil.Equal(t, "nonce", q.Get("nonce"), f.nonce)
	testutil.Equal(t, "code_challenge_method", q.Get("code_challenge_method"), "S256")
	testutil.Equal(t, "code_challenge", q.Get("code_challenge"), base64.RawURLEncoding.EncodeToString(challenge[:]))
	testutil.True(t, "the verifier never leaves the server", !strings.Contains(u.String(), f.verifier))
	testutil.Equal(t, "no client secret in the URL", q.Get("client_secret"), "")
}

func TestScopesAlwaysIncludeOpenID(t *testing.T) {
	t.Parallel()

	_, provider := setup(t, func(c *oidc.Config) { c.Scopes = []string{"email", "groups"} })

	u, err := url.Parse(provider.AuthURL("s", "n", "v"))
	testutil.NoError(t, err)
	testutil.Equal(t, "scope", u.Query().Get("scope"), "openid email groups")
}

// Dex puts the groups claim in the ID token only when the groups scope is
// asked for, and says so in discovery; a provider that does not list the
// scope never gets it, since some (Google among them) refuse a scope they do
// not know.
func TestDefaultScopesAskForAdvertisedGroups(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		advertised []string
		configured []string
		want       string
	}{
		{"groups advertised", strings.Fields("openid email groups profile"), nil, "openid email profile groups"},
		{"groups not advertised", strings.Fields("openid email profile"), nil, "openid email profile"},
		{"no scopes_supported", nil, nil, "openid email profile"},
		{"configured scopes win", strings.Fields("openid groups"), strings.Fields("email"), "openid email"},
	} {
		server := oidctest.New(t, clientID, clientSecret)
		server.AdvertiseScopes(tc.advertised...)

		c := config(server.Issuer())
		c.Scopes = tc.configured

		provider, err := oidc.New(t.Context(), c)
		testutil.NoError(t, err)

		u, err := url.Parse(provider.AuthURL("s", "n", "v"))
		testutil.NoError(t, err)
		testutil.Equal(t, "scope with "+tc.name, u.Query().Get("scope"), tc.want)
	}
}

func TestExchangeRefusesTheWrongFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(f *flow, other flow)
		wantErr error
	}{
		{"another verifier", func(f *flow, other flow) { f.verifier = other.verifier }, nil},
		{"no verifier", func(f *flow, _ flow) { f.verifier = "" }, nil},
		{"another nonce", func(f *flow, other flow) { f.nonce = other.nonce }, oidc.ErrNonce},
		{"no nonce", func(f *flow, _ flow) { f.nonce = "" }, oidc.ErrNonce},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, provider := setup(t)
			f, other := newFlow(t), newFlow(t)

			code, _ := server.Authorize(t, provider.AuthURL(f.state, f.nonce, f.verifier))
			tt.mutate(&f, other)

			_, err := provider.Exchange(t.Context(), code, f.verifier, f.nonce)
			testutil.Error(t, err, tt.name)
			t.Logf("refused: %v", err)

			if tt.wantErr != nil {
				testutil.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestCodeIsUsedOnce(t *testing.T) {
	t.Parallel()

	server, provider := setup(t)
	f := newFlow(t)

	code, _ := server.Authorize(t, provider.AuthURL(f.state, f.nonce, f.verifier))

	_, err := provider.Exchange(t.Context(), code, f.verifier, f.nonce)
	testutil.NoError(t, err)

	_, err = provider.Exchange(t.Context(), code, f.verifier, f.nonce)
	testutil.Error(t, err, "second exchange of one code")
}

func TestWrongClientSecret(t *testing.T) {
	t.Parallel()

	server := oidctest.New(t, clientID, clientSecret)
	c := config(server.Issuer())
	c.ClientSecret = "wrong"

	provider, err := oidc.New(t.Context(), c)
	testutil.NoError(t, err)

	_, err = signIn(t, server, provider)
	testutil.Error(t, err, "wrong client secret")
}

func TestIDTokenChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		breakIt func(*oidctest.Server)
		wantErr error
	}{
		{"other audience", tamper(func(c oidctest.Claims) { c["aud"] = anotherClient }), nil},
		{"no audience", tamper(func(c oidctest.Claims) { delete(c, "aud") }), nil},
		{
			"several audiences without azp",
			tamper(func(c oidctest.Claims) { c["aud"] = []string{clientID, anotherClient} }),
			oidc.ErrAuthorizedParty,
		},
		{
			"several audiences, azp another client",
			tamper(func(c oidctest.Claims) {
				c["aud"] = []string{clientID, anotherClient}
				c["azp"] = anotherClient
			}),
			oidc.ErrAuthorizedParty,
		},
		{"expired", tamper(func(c oidctest.Claims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }), nil},
		{"no expiry", tamper(func(c oidctest.Claims) { delete(c, "exp") }), nil},
		{"not yet valid", tamper(func(c oidctest.Claims) { c["nbf"] = time.Now().Add(time.Hour).Unix() }), nil},
		{"other issuer", tamper(func(c oidctest.Claims) { c["iss"] = "https://sso.example.com" }), nil},
		{"no nonce", tamper(func(c oidctest.Claims) { delete(c, "nonce") }), oidc.ErrNonce},
		{"replayed nonce", tamper(func(c oidctest.Claims) { c["nonce"] = "an-older-nonce" }), oidc.ErrNonce},
		{"no subject", tamper(func(c oidctest.Claims) { delete(c, "sub") }), oidc.ErrNoSubject},
		{"other signing key", (*oidctest.Server).SignWithOtherKey, nil},
		{"unsigned", (*oidctest.Server).Unsigned, nil},
		{"no ID token", (*oidctest.Server).OmitIDToken, oidc.ErrNoIDToken},
		{"groups claim not strings", tamper(func(c oidctest.Claims) { c[claimGroups] = []int{1, 2} }), oidc.ErrGroupsClaim},
		{
			"groups claim an object",
			tamper(func(c oidctest.Claims) { c[claimGroups] = map[string]bool{"a": true} }),
			oidc.ErrGroupsClaim,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, provider := setup(t)
			tt.breakIt(server)

			var none access.Identity

			id, err := signIn(t, server, provider)
			testutil.Error(t, err, tt.name)
			testutil.DeepEqual(t, "identity", id, none)
			t.Logf("refused: %v", err)

			if tt.wantErr != nil {
				testutil.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestExpiredErrorIsTyped(t *testing.T) {
	t.Parallel()

	server, provider := setup(t)
	server.Tamper(func(c oidctest.Claims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })

	_, err := signIn(t, server, provider)

	_, ok := errors.AsType[*gooidc.TokenExpiredError](err)
	testutil.True(t, "TokenExpiredError", ok)
}

func TestSeveralAudiencesWithThisAuthorizedParty(t *testing.T) {
	t.Parallel()

	server, provider := setup(t)
	server.Tamper(func(c oidctest.Claims) {
		c["aud"] = []string{clientID, anotherClient}
		c["azp"] = clientID
	})

	id, err := signIn(t, server, provider)
	testutil.NoError(t, err)
	testutil.Equal(t, "subject", id.Subject, "user-1")
}

func TestEmailVerified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		verified any
		assume   bool
		want     string
	}{
		// A provider that never sends email_verified may let people set
		// any email, so absent is unverified unless the provider is trusted.
		{"absent", nil, false, ""},
		{"absent and assumed", nil, true, editorEmail},
		{"true", true, false, editorEmail},
		{"string true", "true", false, editorEmail},
		{"false", false, false, ""},
		{"false though assumed", false, true, ""},
		{"string false", "false", false, ""},
		{"garbage", "maybe", false, ""},
		{"number", 1, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, provider := setup(t, func(c *oidc.Config) { c.AssumeEmailVerified = tt.assume })
			claims := oidctest.Claims{claimSub: "user-1", claimEmail: editorEmail}

			if tt.verified != nil {
				claims["email_verified"] = tt.verified
			}

			server.SignInAs(claims)

			id, err := signIn(t, server, provider)
			testutil.NoError(t, err)
			testutil.Equal(t, "email", id.Email, tt.want)
		})
	}
}

func TestGroupsClaim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		claim  string
		claims oidctest.Claims
		want   []string
	}{
		{"default claim", "", oidctest.Claims{claimGroups: []string{"a", "b"}}, []string{"a", "b"}},
		{"custom claim", claimRoles, oidctest.Claims{claimRoles: []string{"a"}, claimGroups: []string{"b"}}, []string{"a"}},
		{
			"nested path", "realm_access.roles",
			oidctest.Claims{"realm_access": map[string]any{claimRoles: []string{platform}}}, []string{platform},
		},
		{
			"namespaced claim with dots", "https://example.com/groups",
			oidctest.Claims{"https://example.com/groups": []string{"editors"}}, []string{"editors"},
		},
		{"single string", "", oidctest.Claims{claimGroups: platform}, []string{platform}},
		{"absent", "", oidctest.Claims{}, nil},
		{"null", "", oidctest.Claims{claimGroups: nil}, nil},
		{"empty list", "", oidctest.Claims{claimGroups: []string{}}, []string{}},
		{"empty names dropped", "", oidctest.Claims{claimGroups: []string{"", "a"}}, []string{"a"}},
		{"path through a non-object", "realm_access.roles", oidctest.Claims{"realm_access": "x"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, provider := setup(t, func(c *oidc.Config) { c.GroupsClaim = tt.claim })

			claims := oidctest.Claims{claimSub: "user-1"}
			maps.Copy(claims, tt.claims)

			server.SignInAs(claims)

			id, err := signIn(t, server, provider)
			testutil.NoError(t, err)
			testutil.DeepEqual(t, "groups", id.Groups, tt.want)
		})
	}
}

func TestGroups(t *testing.T) {
	t.Parallel()

	all := map[string]json.RawMessage{
		claimGroups: json.RawMessage(`["a","b"]`),
		"bad":       json.RawMessage(`42`),
		"nested":    json.RawMessage(`{"deeper":{"roles":"x"}}`),
	}

	got, err := oidc.Groups(all, "groups")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "groups", got, []string{"a", "b"})

	got, err = oidc.Groups(all, "nested.deeper.roles")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "nested", got, []string{"x"})

	_, err = oidc.Groups(all, "bad")
	testutil.ErrorIs(t, err, oidc.ErrGroupsClaim)

	got, err = oidc.Groups(all, "missing.path")
	testutil.NoError(t, err)
	testutil.True(t, "missing path", got == nil)
}

func TestNewRefuses(t *testing.T) {
	t.Parallel()

	server := oidctest.New(t, clientID, clientSecret)

	tests := []struct {
		name   string
		change func(*oidc.Config)
		want   error
	}{
		{"no client ID", func(c *oidc.Config) { c.ClientID = "" }, oidc.ErrConfig},
		{"no issuer", func(c *oidc.Config) { c.Issuer = "" }, oidc.ErrConfig},
		{"relative issuer", func(c *oidc.Config) { c.Issuer = "/realms/main" }, oidc.ErrConfig},
		{"no redirect URL", func(c *oidc.Config) { c.RedirectURL = "" }, oidc.ErrConfig},
		{"redirect URL without host", func(c *oidc.Config) { c.RedirectURL = "https:///auth/callback" }, oidc.ErrConfig},
		{"redirect URL with another scheme", func(c *oidc.Config) { c.RedirectURL = "ftp://example.com/cb" }, oidc.ErrConfig},
		{"issuer without discovery", func(c *oidc.Config) { c.Issuer = server.Issuer() + "/nothing" }, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := config(server.Issuer())
			tt.change(&c)

			_, err := oidc.New(t.Context(), c)
			testutil.Error(t, err, tt.name)

			if tt.want != nil {
				testutil.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestNewRefusesAnotherIssuer(t *testing.T) {
	t.Parallel()

	server := oidctest.New(t, clientID, clientSecret)
	server.AdvertiseIssuer("https://sso.example.com")

	_, err := oidc.New(t.Context(), config(server.Issuer()))

	_, ok := errors.AsType[*gooidc.IssuerMismatchError](err)
	testutil.True(t, "IssuerMismatchError", ok)
}

func TestPublicClient(t *testing.T) {
	t.Parallel()

	server := oidctest.New(t, clientID, "")
	c := config(server.Issuer())
	c.ClientSecret = ""

	provider, err := oidc.New(t.Context(), c)
	testutil.NoError(t, err)

	_, err = signIn(t, server, provider)
	testutil.NoError(t, err)
}

func tamper(change func(oidctest.Claims)) func(*oidctest.Server) {
	return func(s *oidctest.Server) { s.Tamper(change) }
}
