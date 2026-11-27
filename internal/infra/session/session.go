// Package session keeps the server's sign-in state in cookies the browser
// holds and cannot read or change: every value is JSON sealed with AES-GCM
// under one 32-byte key, so the server keeps no session store. It has two
// cookies: the session itself, which carries the identity, its expiry, the
// CSRF token bound to it and the chosen language, and the short-lived flow
// cookie, which carries the OpenID Connect state, nonce, PKCE verifier and
// the page to return to between the redirect to the provider and the
// callback.
package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
)

const (
	// KeySize is the length of the key in bytes: AES-256.
	KeySize = 32
	// FlowTTL is how long a sign-in may take between the redirect to the
	// provider and the callback.
	FlowTTL = 10 * time.Minute
	// MaxCookieSize is the longest Set-Cookie value written. Browsers drop a
	// cookie past about 4096 bytes without a word, so Set refuses one instead.
	MaxCookieSize = 4096
	// CSRFField is the form field that carries the CSRF token.
	CSRFField = "csrf"
	// CSRFHeader is the request header that carries the CSRF token, for htmx
	// requests. Header names are case-insensitive, so X-CSRF-Token matches it.
	CSRFHeader = "X-Csrf-Token"
	// DefaultReturnPath is where a sign-in returns when it was given no safe
	// path.
	DefaultReturnPath = "/"

	// sessionPurpose and flowPurpose name the two cookies and are sealed into
	// each value as additional data, so one cookie's value is refused as the
	// other's.
	sessionPurpose = "wpfleet_session"
	flowPurpose    = "wpfleet_flow"
	// hostPrefix makes the browser refuse the cookie unless it is Secure, has
	// Path=/ and no Domain, so a sibling subdomain cannot set it.
	hostPrefix = "__Host-"
	// tokenBytes is the randomness of a CSRF token, state, nonce and PKCE
	// verifier. As base64url it is 43 characters, which RFC 7636 asks of a
	// verifier.
	tokenBytes = 32
	// maxReturnPath is the longest return path kept.
	maxReturnPath = 2048
	// authPrefix is the path of the sign-in routes; returning there would loop.
	authPrefix = "/auth/"
)

// ErrKeySize is returned for a key that is not KeySize bytes.
var ErrKeySize = fmt.Errorf("session key must be %d bytes", KeySize)

// ErrMaxAge is returned for a maximum age that is not positive.
var ErrMaxAge = errors.New("session max age must be positive")

// ErrTooLarge is returned when a sealed cookie would pass MaxCookieSize,
// typically for a person in very many groups.
var ErrTooLarge = errors.New("session cookie too large")

// ErrNotBase64 is returned by DecodeKey for a key that is not base64.
var ErrNotBase64 = errors.New("session key is not base64")

// errShort is returned for a cookie shorter than a nonce and a tag.
var errShort = errors.New("cookie too short")

// ErrInvalid is returned by Set for a session the server must never issue:
// one with no subject, no CSRF token or an expiry that has passed.
var ErrInvalid = errors.New("invalid session")

// Session is the state of one signed-in person.
type Session struct {
	// Identity is the person, as the provider described them at sign-in.
	Identity access.Identity
	// Expires is when the session ends and the person signs in again, which
	// reads their groups again.
	Expires time.Time
	// CSRF is the token every request that changes something must carry.
	CSRF string
	// Lang is the UI language the person chose; empty follows the browser.
	Lang string
}

// Flow is the state of one sign-in between the redirect to the provider and
// the callback.
type Flow struct {
	// State is the OAuth 2.0 state the callback must return.
	State string
	// Nonce is the OpenID Connect nonce the ID token must carry.
	Nonce string
	// Verifier is the PKCE code verifier.
	Verifier string
	// ReturnTo is the same-site path to open after the sign-in.
	ReturnTo string
	// Lang is the UI language the person picked before signing in, which
	// the session starts with; empty follows the browser.
	Lang string
	// Expires is when the flow ends.
	Expires time.Time
}

// NewFlow returns the state of a new sign-in: a fresh state, nonce and PKCE
// verifier, and returnTo when it is a safe same-site path, else
// DefaultReturnPath. It ends after FlowTTL.
func NewFlow(returnTo string) (Flow, error) {
	var tokens [3]string

	for i := range tokens {
		token, err := Token()
		if err != nil {
			return Flow{}, err
		}

		tokens[i] = token
	}

	return Flow{
		State:    tokens[0],
		Nonce:    tokens[1],
		Verifier: tokens[2],
		ReturnTo: SafeReturnPath(returnTo),
		Lang:     "",
		Expires:  time.Now().Add(FlowTTL).Truncate(time.Second),
	}, nil
}

// CheckState reports, in constant time, whether state is the flow's state.
func (f Flow) CheckState(state string) bool {
	return f.State != "" && subtle.ConstantTimeCompare([]byte(f.State), []byte(state)) == 1
}

// sessionPayload is the sealed form of a Session.
type sessionPayload struct {
	Identity access.Identity `json:"id"`
	Expires  int64           `json:"exp"`
	CSRF     string          `json:"csrf"`
	Lang     string          `json:"lang,omitempty"`
}

// flowPayload is the sealed form of a Flow.
type flowPayload struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	ReturnTo string `json:"return"`
	Lang     string `json:"lang,omitempty"`
	Expires  int64  `json:"exp"`
}

// Manager reads and writes the two cookies. It is safe for concurrent use.
type Manager struct {
	aead   cipher.AEAD
	maxAge time.Duration
	secure bool
}

// NewManager returns a manager that seals cookies under key, ends a session
// after maxAge, and marks the cookies Secure (with the __Host- prefix) when
// secure is true. Only a server reached over plain HTTP, such as a local
// test, passes false.
func NewManager(key []byte, maxAge time.Duration, secure bool) (*Manager, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}

	if maxAge <= 0 {
		return nil, ErrMaxAge
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}

	return &Manager{aead: aead, maxAge: maxAge, secure: secure}, nil
}

// DecodeKey reads a key given as base64, standard or URL-safe, padded or
// not, with surrounding white space ignored, and checks its length.
func DecodeKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)

	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		key, err := enc.DecodeString(encoded)
		if err != nil {
			continue
		}

		if len(key) != KeySize {
			return nil, fmt.Errorf("%w, got %d", ErrKeySize, len(key))
		}

		return key, nil
	}

	return nil, ErrNotBase64
}

// New returns a fresh session for id that ends after the manager's maximum
// age, with a new CSRF token.
func (m *Manager) New(id access.Identity, lang string) (Session, error) {
	csrf, err := Token()
	if err != nil {
		return Session{}, err
	}

	return Session{
		Identity: id,
		Expires:  time.Now().Add(m.maxAge).Truncate(time.Second),
		CSRF:     csrf,
		Lang:     lang,
	}, nil
}

// Get returns the request's session. It reports false for a missing cookie,
// one sealed under another key or for the other cookie, a changed one, one
// whose expiry has passed, and one that would outlive the manager's maximum
// age, which happens after the maximum age was lowered.
func (m *Manager) Get(r *http.Request) (Session, bool) {
	var none Session

	plain, ok := m.read(r, sessionPurpose)
	if !ok {
		return none, false
	}

	var p sessionPayload

	err := json.Unmarshal(plain, &p)
	if err != nil {
		return none, false
	}

	expires := time.Unix(p.Expires, 0)
	now := time.Now()

	if !now.Before(expires) || expires.After(now.Add(m.maxAge)) || p.Identity.Subject == "" || p.CSRF == "" {
		return none, false
	}

	return Session{Identity: p.Identity, Expires: expires, CSRF: p.CSRF, Lang: p.Lang}, true
}

// Set writes s as the session cookie. It refuses with ErrInvalid a session
// with no subject, no CSRF token or a passed expiry, and with ErrTooLarge
// one the browser would drop.
func (m *Manager) Set(w http.ResponseWriter, s Session) error {
	if s.Identity.Subject == "" || s.CSRF == "" || !time.Now().Before(s.Expires) {
		return ErrInvalid
	}

	plain, err := json.Marshal(sessionPayload{
		Identity: s.Identity,
		Expires:  s.Expires.Unix(),
		CSRF:     s.CSRF,
		Lang:     s.Lang,
	})
	if err != nil {
		return fmt.Errorf("encoding the session: %w", err)
	}

	return m.write(w, sessionPurpose, plain, s.Expires)
}

// Clear removes the session cookie, which signs the person out.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, m.expired(sessionPurpose))
}

// CheckCSRF reports, in constant time, whether the request carries the
// session's CSRF token in the X-CSRF-Token header or, without the header, in
// the csrf form field of a POST body.
func (m *Manager) CheckCSRF(r *http.Request, s Session) bool {
	token := r.Header.Get(CSRFHeader)
	if token == "" {
		token = r.PostFormValue(CSRFField)
	}

	return s.CSRF != "" && token != "" && subtle.ConstantTimeCompare([]byte(s.CSRF), []byte(token)) == 1
}

// SetFlow writes f as the flow cookie. A second sign-in started before the
// first returns replaces it, so the first one's callback is refused.
func (m *Manager) SetFlow(w http.ResponseWriter, f Flow) error {
	if f.State == "" || f.Nonce == "" || f.Verifier == "" || !time.Now().Before(f.Expires) {
		return ErrInvalid
	}

	plain, err := json.Marshal(flowPayload{
		State:    f.State,
		Nonce:    f.Nonce,
		Verifier: f.Verifier,
		ReturnTo: SafeReturnPath(f.ReturnTo),
		Lang:     f.Lang,
		Expires:  f.Expires.Unix(),
	})
	if err != nil {
		return fmt.Errorf("encoding the sign-in flow: %w", err)
	}

	return m.write(w, flowPurpose, plain, f.Expires)
}

// TakeFlow returns the request's flow and removes the cookie, so a flow is
// used at most once. It reports false when there is none or it is not valid
// or has expired.
func (m *Manager) TakeFlow(w http.ResponseWriter, r *http.Request) (Flow, bool) {
	var none Flow

	plain, ok := m.read(r, flowPurpose)
	if !ok {
		return none, false
	}

	http.SetCookie(w, m.expired(flowPurpose))

	var p flowPayload

	err := json.Unmarshal(plain, &p)
	if err != nil {
		return none, false
	}

	expires := time.Unix(p.Expires, 0)
	now := time.Now()

	if !now.Before(expires) || expires.After(now.Add(FlowTTL)) || p.State == "" || p.Nonce == "" || p.Verifier == "" {
		return none, false
	}

	return Flow{
		State:    p.State,
		Nonce:    p.Nonce,
		Verifier: p.Verifier,
		ReturnTo: SafeReturnPath(p.ReturnTo),
		Lang:     p.Lang,
		Expires:  expires,
	}, true
}

// SafeReturnPath returns raw when it is a path on this site that is safe to
// redirect to after a sign-in, else DefaultReturnPath. It refuses absolute
// and scheme-relative URLs ("//host", "/\host"), back slashes and control
// characters, which browsers turn into those, and the sign-in routes
// themselves.
func SafeReturnPath(raw string) string {
	if !relativePath(raw) {
		return DefaultReturnPath
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" || authPath(u.Path) {
		return DefaultReturnPath
	}

	u.Fragment = ""
	u.RawFragment = ""

	return u.String()
}

// relativePath reports whether raw looks like a path on this site: it starts
// with one slash, is not too long, and holds no back slash or control
// character.
func relativePath(raw string) bool {
	if raw == "" || len(raw) > maxReturnPath || raw[0] != '/' {
		return false
	}

	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return false
	}

	return !strings.ContainsFunc(raw, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f })
}

// authPath reports whether path is one of the sign-in routes.
func authPath(path string) bool {
	return path == strings.TrimSuffix(authPrefix, "/") || strings.HasPrefix(path, authPrefix)
}

// Token returns 32 random bytes as unpadded base64url: a CSRF token, an
// OAuth 2.0 state, a nonce or a PKCE verifier.
func Token() (string, error) {
	b := make([]byte, tokenBytes)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// cookieName is the name of the cookie for purpose.
func (m *Manager) cookieName(purpose string) string {
	if m.secure {
		return hostPrefix + purpose
	}

	return purpose
}

// cookie returns the cookie for purpose with every attribute set.
func (m *Manager) cookie(purpose, value string, expires time.Time, maxAge int) *http.Cookie {
	//nolint:gosec // HttpOnly and SameSite are always set; Secure is off only for a plain-HTTP server
	return &http.Cookie{
		Name:        m.cookieName(purpose),
		Value:       value,
		Quoted:      false,
		Path:        "/",
		Domain:      "",
		Expires:     expires,
		RawExpires:  "",
		MaxAge:      maxAge,
		Secure:      m.secure,
		HttpOnly:    true,
		SameSite:    http.SameSiteLaxMode,
		Partitioned: false,
		Raw:         "",
		Unparsed:    nil,
	}
}

// expired returns a cookie that makes the browser delete purpose's cookie.
func (m *Manager) expired(purpose string) *http.Cookie {
	return m.cookie(purpose, "", time.Unix(0, 0), -1)
}

// write seals plain and sets it as purpose's cookie until expires.
func (m *Manager) write(w http.ResponseWriter, purpose string, plain []byte, expires time.Time) error {
	value, err := m.seal(purpose, plain)
	if err != nil {
		return err
	}

	maxAge := int(time.Until(expires).Round(time.Second) / time.Second)
	if maxAge <= 0 {
		return ErrInvalid
	}

	c := m.cookie(purpose, value, expires, maxAge)
	if len(c.String()) > MaxCookieSize {
		return ErrTooLarge
	}

	http.SetCookie(w, c)

	return nil
}

// read opens purpose's cookie of the request.
func (m *Manager) read(r *http.Request, purpose string) ([]byte, bool) {
	c, err := r.Cookie(m.cookieName(purpose))
	if err != nil {
		return nil, false
	}

	plain, err := m.open(purpose, c.Value)
	if err != nil {
		return nil, false
	}

	return plain, true
}

// seal encrypts plain with a random nonce, binding it to purpose.
func (m *Manager) seal(purpose string, plain []byte) (string, error) {
	nonce := make([]byte, m.aead.NonceSize(), m.aead.NonceSize()+len(plain)+m.aead.Overhead())
	_, err := rand.Read(nonce)
	if err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(m.aead.Seal(nonce, nonce, plain, []byte(purpose))), nil
}

// open decrypts a value seal made for purpose.
func (m *Manager) open(purpose, value string) ([]byte, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decoding the cookie: %w", err)
	}

	if len(sealed) < m.aead.NonceSize()+m.aead.Overhead() {
		return nil, errShort
	}

	nonce, ciphertext := sealed[:m.aead.NonceSize()], sealed[m.aead.NonceSize():]

	plain, err := m.aead.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return nil, fmt.Errorf("opening the cookie: %w", err)
	}

	return plain, nil
}
