// Package oidc signs people in with any OpenID Connect provider that
// publishes discovery (Keycloak, Dex, Azure AD, Google). It runs the
// authorization code flow with PKCE (S256), state and nonce, verifies the ID
// token's signature, issuer, audience, expiry and nonce, and reads the
// subject, email, name and the configured groups claim into an
// access.Identity. Nothing else about the person is read.
package oidc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/rahacloud/wpfleet/internal/domain/access"
)

// DefaultGroupsClaim is the ID token claim that carries the groups when the
// configuration names none.
const DefaultGroupsClaim = "groups"

// ErrConfig is wrapped by every error about the configuration.
var ErrConfig = errors.New("invalid OpenID Connect configuration")

// ErrNoIDToken is returned when the token response carries no ID token.
var ErrNoIDToken = errors.New("token response has no id_token")

// ErrNonce is returned when the ID token's nonce is not the one the sign-in
// sent, or either is empty.
var ErrNonce = errors.New("ID token nonce does not match")

// ErrAuthorizedParty is returned for an ID token issued to several audiences
// whose authorized party (azp) is not this client.
var ErrAuthorizedParty = errors.New("ID token was not issued to this client")

// ErrNoSubject is returned for an ID token with no subject.
var ErrNoSubject = errors.New("ID token has no subject")

// ErrGroupsClaim is returned when the groups claim is neither a string nor a
// list of strings.
var ErrGroupsClaim = errors.New("groups claim is not a list of strings")

// DefaultScopes are the scopes requested when the configuration names none.
func DefaultScopes() []string {
	return []string{gooidc.ScopeOpenID, "email", "profile"}
}

// Config is how the server talks to the provider.
type Config struct {
	// Issuer is the provider's issuer URL; discovery is read from
	// <Issuer>/.well-known/openid-configuration.
	Issuer string
	// ClientID is the server's client at the provider, and the audience the ID
	// token must name.
	ClientID string
	// ClientSecret is the client's secret, from WPFLEET_OIDC_CLIENT_SECRET.
	// Empty makes the server a public client that relies on PKCE alone.
	ClientSecret string
	// RedirectURL is <publicURL>/auth/callback.
	RedirectURL string
	// GroupsClaim is the claim that carries the groups; empty is
	// DefaultGroupsClaim. A name with dots that is not itself a claim is read
	// as a path into nested objects, e.g. realm_access.roles.
	GroupsClaim string
	// Scopes are the requested scopes; empty is DefaultScopes. openid is
	// always requested.
	Scopes []string
}

// Validate reports a configuration that cannot work.
func (c Config) Validate() error {
	var errs []error

	if c.ClientID == "" {
		errs = append(errs, fmt.Errorf("%w: client ID is empty", ErrConfig))
	}

	for name, raw := range map[string]string{"issuer": c.Issuer, "redirect URL": c.RedirectURL} {
		u, err := url.Parse(raw)
		if raw == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%w: %s %q is not an absolute HTTP(S) URL", ErrConfig, name, raw))
		}
	}

	return errors.Join(errs...)
}

// Provider is a discovered provider and this server's client at it.
type Provider struct {
	oauth       oauth2.Config
	verifier    *gooidc.IDTokenVerifier
	clientID    string
	groupsClaim string
}

// New validates c and reads the provider's discovery document. The HTTP
// client in ctx (oauth2.HTTPClient), if any, is used for discovery and for
// fetching the signing keys later.
func New(ctx context.Context, c Config) (*Provider, error) {
	err := c.Validate()
	if err != nil {
		return nil, err
	}

	provider, err := gooidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering %s: %w", c.Issuer, err)
	}

	scopes := c.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes()
	}

	if !slices.Contains(scopes, gooidc.ScopeOpenID) {
		scopes = append([]string{gooidc.ScopeOpenID}, scopes...)
	}

	groupsClaim := c.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = DefaultGroupsClaim
	}

	return &Provider{
		oauth: oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  c.RedirectURL,
			Scopes:       scopes,
		},
		verifier: provider.Verifier(&gooidc.Config{ //nolint:exhaustruct_v5 // the zero values keep every check on
			ClientID: c.ClientID,
		}),
		clientID:    c.ClientID,
		groupsClaim: groupsClaim,
	}, nil
}

// AuthURL returns the provider's authorization URL for one sign-in: the code
// flow with state, nonce and the S256 challenge of verifier.
func (p *Provider) AuthURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

// Exchange trades the callback's code for tokens, proving the sign-in with
// verifier, and returns the person the ID token names. It refuses an ID token
// that is missing, not signed by the provider's keys, issued by another
// issuer, for another audience (or, with several audiences, to another
// authorized party), expired, without a subject, or whose nonce is not nonce.
// An email the provider marks as not verified is dropped, so it never
// matches a WordPress user.
func (p *Provider) Exchange(ctx context.Context, code, verifier, nonce string) (access.Identity, error) {
	var none access.Identity

	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return none, fmt.Errorf("exchanging the code: %w", err)
	}

	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return none, ErrNoIDToken
	}

	idToken, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return none, fmt.Errorf("verifying the ID token: %w", err)
	}

	if nonce == "" || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 {
		return none, ErrNonce
	}

	return p.identity(idToken)
}

// claims are the standard claims Exchange reads beside the groups.
type claims struct {
	AuthorizedParty string          `json:"azp"`
	Email           string          `json:"email"`
	EmailVerified   json.RawMessage `json:"email_verified"`
	Name            string          `json:"name"`
}

// identity reads the person from a verified ID token.
func (p *Provider) identity(idToken *gooidc.IDToken) (access.Identity, error) {
	var none access.Identity

	if idToken.Subject == "" {
		return none, ErrNoSubject
	}

	var std claims

	err := idToken.Claims(&std)
	if err != nil {
		return none, fmt.Errorf("reading the ID token claims: %w", err)
	}

	if len(idToken.Audience) > 1 && std.AuthorizedParty != p.clientID {
		return none, ErrAuthorizedParty
	}

	var all map[string]json.RawMessage

	err = idToken.Claims(&all)
	if err != nil {
		return none, fmt.Errorf("reading the ID token claims: %w", err)
	}

	groups, err := Groups(all, p.groupsClaim)
	if err != nil {
		return none, err
	}

	email := std.Email
	if !verified(std.EmailVerified) {
		email = ""
	}

	return access.Identity{Subject: idToken.Subject, Email: email, Name: std.Name, Groups: groups}, nil
}

// verified reads email_verified. Absent counts as verified, since several
// providers (Azure AD among them) never send it; false, as a boolean or as the
// string some providers send, does not.
func verified(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}

	var b bool

	err := json.Unmarshal(raw, &b)
	if err == nil {
		return b
	}

	var s string

	err = json.Unmarshal(raw, &s)
	if err != nil {
		return false
	}

	b, err = strconv.ParseBool(s)

	return err == nil && b
}

// Groups reads the groups claim named name from an ID token's claims: a list
// of strings, or a single string as some providers send for one group. A
// missing or null claim is no groups. When name is not a claim and holds
// dots, it is followed as a path through nested objects.
func Groups(all map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := lookup(all, name)
	if !ok || string(raw) == "null" {
		return nil, nil
	}

	var list []string

	err := json.Unmarshal(raw, &list)
	if err == nil {
		return slices.DeleteFunc(list, func(g string) bool { return g == "" }), nil
	}

	var one string

	err = json.Unmarshal(raw, &one)
	if err == nil {
		if one == "" {
			return nil, nil
		}

		return []string{one}, nil
	}

	return nil, fmt.Errorf("%w: %s", ErrGroupsClaim, name)
}

// lookup finds the claim name, first as a key of its own (a namespaced claim
// such as https://example.com/groups has dots), then as a dotted path.
func lookup(all map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if raw, ok := all[name]; ok {
		return raw, true
	}

	head, rest, found := strings.Cut(name, ".")
	if !found {
		return nil, false
	}

	raw, ok := all[head]
	if !ok {
		return nil, false
	}

	var nested map[string]json.RawMessage

	err := json.Unmarshal(raw, &nested)
	if err != nil {
		return nil, false
	}

	return lookup(nested, rest)
}
