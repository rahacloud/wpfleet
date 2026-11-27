// Package oidctest is an OpenID Connect provider for tests: discovery, a
// JWKS with a generated RSA key, an authorization endpoint that signs the
// next person in without a login page, and a token endpoint that checks the
// client, the redirect URI and the PKCE verifier before it issues an RS256 ID
// token. Tests choose who signs in and can break the ID token on purpose.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// keyBits is the size of the generated RSA keys.
const keyBits = 2048

// codeBytes is the randomness of an authorization code and an access token.
const codeBytes = 16

// algRS256 is the only signing algorithm the server uses.
const algRS256 = "RS256"

// errorKey is the field of an OAuth 2.0 error response.
const errorKey = "error"

// TokenTTL is how long the ID tokens the server issues are valid.
const TokenTTL = 5 * time.Minute

// Claims are the claims of an ID token, a JSON object.
type Claims = map[string]any

// keys are generated once per test binary, since RSA key generation is slow.
//
//nolint:gochecknoglobals // a lazily generated cache shared by every Server of the test binary
var keys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	var out [2]*rsa.PrivateKey

	for i := range out {
		key, err := rsa.GenerateKey(rand.Reader, keyBits)
		if err != nil {
			panic(err)
		}

		out[i] = key
	}

	return out
})

// grant is one authorization the token endpoint can redeem once.
type grant struct {
	clientID    string
	redirectURI string
	challenge   string
	nonce       string
	person      Claims
}

// Server is the fake provider. Its zero value is not usable; call New.
type Server struct {
	// ClientID is the only client the server knows.
	ClientID string
	// ClientSecret is that client's secret; empty accepts a public client.
	ClientSecret string

	srv *httptest.Server

	mu         sync.Mutex
	codes      map[string]grant
	person     Claims
	tamper     func(Claims)
	otherKey   bool
	unsigned   bool
	noIDToken  bool
	issuer     string
	tokenCalls int
}

// New starts a provider for one client and stops it when the test ends. The
// first person to sign in has the subject "user-1" and no other claims.
func New(tb testing.TB, clientID, clientSecret string) *Server {
	tb.Helper()

	s := &Server{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		srv:          nil,
		mu:           sync.Mutex{},
		codes:        map[string]grant{},
		person:       Claims{"sub": "user-1"},
		tamper:       nil,
		otherKey:     false,
		unsigned:     false,
		noIDToken:    false,
		issuer:       "",
		tokenCalls:   0,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /keys", s.jwks)
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)

	s.srv = httptest.NewServer(mux)
	tb.Cleanup(s.srv.Close)

	return s
}

// Issuer is the provider's issuer URL.
func (s *Server) Issuer() string { return s.srv.URL }

// SignInAs makes claims the person of the next sign-ins, e.g. sub, email,
// name and groups. The server adds iss, aud, exp, iat and nonce.
func (s *Server) SignInAs(claims Claims) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.person = claims
}

// Tamper calls change on the claims of every ID token the server issues from
// now on, after it has filled them in, so a test can make one invalid.
func (s *Server) Tamper(change func(Claims)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tamper = change
}

// SignWithOtherKey signs ID tokens with a key the JWKS does not publish.
func (s *Server) SignWithOtherKey() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.otherKey = true
}

// Unsigned issues ID tokens with "alg": "none" and no signature.
func (s *Server) Unsigned() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.unsigned = true
}

// OmitIDToken leaves the ID token out of the token response.
func (s *Server) OmitIDToken() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.noIDToken = true
}

// AdvertiseIssuer makes discovery claim another issuer than the server's URL.
func (s *Server) AdvertiseIssuer(issuer string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.issuer = issuer
}

// TokenCalls is how many requests reached the token endpoint.
func (s *Server) TokenCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tokenCalls
}

// Authorize opens authURL as a browser would and returns the code and state
// the provider redirects back with. It stops the test when the provider
// refuses the request.
func (s *Server) Authorize(tb testing.TB, authURL string) (string, string) {
	tb.Helper()

	location := s.Redirect(tb, authURL)

	callback, err := url.Parse(location)
	if err != nil {
		tb.Fatalf("callback %q: %v", location, err)
	}

	code := callback.Query().Get("code")
	if code == "" {
		tb.Fatalf("callback %q has no code", location)
	}

	return code, callback.Query().Get("state")
}

// Redirect opens authURL without following the redirect and returns where
// the provider sends the browser. It stops the test unless the provider
// answers with a redirect.
func (s *Server) Redirect(tb testing.TB, authURL string) string {
	tb.Helper()

	client := s.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(tb.Context(), http.MethodGet, authURL, nil)
	if err != nil {
		tb.Fatalf("authorization request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		tb.Fatalf("authorization request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		tb.Fatalf("authorization request: status %d, want %d", resp.StatusCode, http.StatusFound)
	}

	return resp.Header.Get("Location")
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	issuer := s.issuer
	s.mu.Unlock()

	if issuer == "" {
		issuer = s.srv.URL
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                s.srv.URL + "/authorize",
		"token_endpoint":                        s.srv.URL + "/token",
		"jwks_uri":                              s.srv.URL + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{algRS256},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := keys()[0].PublicKey

	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": "test",
		"use": "sig",
		"alg": algRS256,
		"n":   b64(pub.N.Bytes()),
		"e":   b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	redirect, err := url.Parse(q.Get("redirect_uri"))

	switch {
	case q.Get("response_type") != "code",
		q.Get("client_id") != s.ClientID,
		err != nil || redirect.Scheme == "" || redirect.Host == "",
		q.Get("code_challenge_method") != "S256",
		q.Get("code_challenge") == "",
		q.Get("code_verifier") != "",
		!strings.Contains(" "+q.Get("scope")+" ", " openid "):
		http.Error(w, "invalid_request", http.StatusBadRequest)

		return
	}

	code := b64(random(codeBytes))

	s.mu.Lock()
	s.codes[code] = grant{
		clientID:    q.Get("client_id"),
		redirectURI: q.Get("redirect_uri"),
		challenge:   q.Get("code_challenge"),
		nonce:       q.Get("nonce"),
		person:      s.person,
	}
	s.mu.Unlock()

	back := redirect.Query()
	back.Set("code", code)
	back.Set("state", q.Get("state"))
	redirect.RawQuery = back.Encode()

	// An identity provider redirects to the client's redirect URI; this fake
	// one trusts any, since it has a single client set up by the test.
	http.Redirect(w, r, redirect.String(), http.StatusFound) //nolint:gosec // see above
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.tokenCalls++
	s.mu.Unlock()

	err := r.ParseForm()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{errorKey: "invalid_request"})

		return
	}

	if !s.clientAuthenticated(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{errorKey: "invalid_client"})

		return
	}

	s.mu.Lock()
	g, ok := s.codes[r.PostForm.Get("code")]
	delete(s.codes, r.PostForm.Get("code"))
	s.mu.Unlock()

	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))

	if r.PostForm.Get("grant_type") != "authorization_code" || !ok ||
		g.redirectURI != r.PostForm.Get("redirect_uri") ||
		subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(g.challenge)) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{errorKey: "invalid_grant"})

		return
	}

	response := map[string]any{
		"access_token": b64(random(codeBytes)),
		"token_type":   "Bearer",
		"expires_in":   int(TokenTTL.Seconds()),
	}

	s.mu.Lock()
	omit := s.noIDToken
	s.mu.Unlock()

	if !omit {
		response["id_token"] = s.idToken(g)
	}

	writeJSON(w, http.StatusOK, response)
}

// clientAuthenticated checks the client by HTTP Basic or by form fields.
func (s *Server) clientAuthenticated(r *http.Request) bool {
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}

	return id == s.ClientID && subtle.ConstantTimeCompare([]byte(secret), []byte(s.ClientSecret)) == 1
}

// idToken builds and signs the ID token of a redeemed grant.
func (s *Server) idToken(g grant) string {
	now := time.Now()

	claims := Claims{}
	maps.Copy(claims, g.person)

	claims["iss"] = s.srv.URL
	claims["aud"] = g.clientID
	claims["iat"] = now.Unix()
	claims["exp"] = now.Add(TokenTTL).Unix()

	if g.nonce != "" {
		claims["nonce"] = g.nonce
	}

	s.mu.Lock()
	tamper, otherKey, unsigned := s.tamper, s.otherKey, s.unsigned
	s.mu.Unlock()

	if tamper != nil {
		tamper(claims)
	}

	return Sign(claims, otherKey, unsigned)
}

// Sign returns claims as a compact JWT signed with RS256 by the key the JWKS
// publishes, or by another key, or unsigned with "alg": "none".
func Sign(claims Claims, otherKey, unsigned bool) string {
	alg := algRS256
	if unsigned {
		alg = "none"
	}

	header := must(json.Marshal(map[string]string{"alg": alg, "typ": "JWT", "kid": "test"}))
	payload := must(json.Marshal(claims))
	signingInput := b64(header) + "." + b64(payload)

	if unsigned {
		return signingInput + "."
	}

	key := keys()[0]
	if otherKey {
		key = keys()[1]
	}

	digest := sha256.Sum256([]byte(signingInput))

	return signingInput + "." + b64(must(rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])))
}

func writeJSON[T any](w http.ResponseWriter, status int, body T) {
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_, _ = w.Write(encoded)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func random(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return b
}

// must panics on an error that only a broken test setup can cause.
func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}

	return b
}
