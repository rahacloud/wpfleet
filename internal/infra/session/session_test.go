package session_test

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/session"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	maxAge     = 8 * time.Hour
	sitePath   = "/sites/staging/blog"
	deployPath = sitePath + "/deploy"
)

func key(fill byte) []byte { return bytes.Repeat([]byte{fill}, session.KeySize) }

func manager(t *testing.T, secure bool) *session.Manager {
	t.Helper()

	m, err := session.NewManager(key(7), maxAge, secure)
	testutil.NoError(t, err)

	return m
}

func identity() access.Identity {
	return access.Identity{
		Subject: "u-123",
		Email:   "editor@example.com",
		Name:    "Example Editor",
		Groups:  []string{"shop-team", "staff"},
	}
}

// issue sets a new session and returns it with the response's cookies.
func issue(t *testing.T, m *session.Manager) (session.Session, []*http.Cookie) {
	t.Helper()

	s, err := m.New(identity(), "fa")
	testutil.NoError(t, err)

	rec := httptest.NewRecorder()
	testutil.NoError(t, m.Set(rec, s))

	return s, rec.Result().Cookies()
}

// sent is a cookie as a browser sends it: a name and a value, no attributes.
func sent(name, value string) *http.Cookie {
	//nolint:exhaustruct_v5,gosec // a request cookie has only a name and a value
	return &http.Cookie{Name: name, Value: value}
}

// request returns a GET request that carries cookies.
func request(t *testing.T, cookies ...*http.Cookie) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for _, c := range cookies {
		r.AddCookie(sent(c.Name, c.Value))
	}

	return r
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	m := manager(t, true)
	want, cookies := issue(t, m)

	got, ok := m.Get(request(t, cookies...))
	testutil.True(t, "Get", ok)
	testutil.DeepEqual(t, "session", got, want)
}

func TestCookieAttributes(t *testing.T) {
	t.Parallel()

	for _, secure := range []bool{true, false} {
		t.Run(fmt.Sprintf("secure=%v", secure), func(t *testing.T) {
			t.Parallel()

			_, cookies := issue(t, manager(t, secure))
			testutil.Equal(t, "cookies", len(cookies), 1)

			c := cookies[0]
			wantName := "wpfleet_session"

			if secure {
				wantName = "__Host-wpfleet_session"
			}

			testutil.Equal(t, "name", c.Name, wantName)
			testutil.True(t, "HttpOnly", c.HttpOnly)
			testutil.Equal(t, "Secure", c.Secure, secure)
			testutil.Equal(t, "SameSite", c.SameSite, http.SameSiteLaxMode)
			testutil.Equal(t, "Path", c.Path, "/")
			testutil.Equal(t, "Domain", c.Domain, "")
			testutil.True(t, "MaxAge about 8h", c.MaxAge > int(maxAge.Seconds())-5 && c.MaxAge <= int(maxAge.Seconds()))
			testutil.NotContains(t, "value", c.Value, "editor@example.com")
		})
	}
}

func TestSecureAndPlainCookiesDoNotMix(t *testing.T) {
	t.Parallel()

	secure, err := session.NewManager(key(7), maxAge, true)
	testutil.NoError(t, err)

	plain, err := session.NewManager(key(7), maxAge, false)
	testutil.NoError(t, err)

	_, cookies := issue(t, plain)

	_, ok := secure.Get(request(t, cookies...))
	testutil.True(t, "a secure manager reads only the __Host- cookie", !ok)
}

func TestGetRefuses(t *testing.T) {
	t.Parallel()

	m := manager(t, false)
	_, cookies := issue(t, m)
	good := cookies[0]

	other, err := session.NewManager(key(8), maxAge, false)
	testutil.NoError(t, err)

	_, otherCookies := issue(t, other)

	raw, err := base64.RawURLEncoding.DecodeString(good.Value)
	testutil.NoError(t, err)

	flipped := bytes.Clone(raw)
	flipped[len(flipped)/2] ^= 1

	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"not base64", "!!!"},
		{"too short", base64.RawURLEncoding.EncodeToString(raw[:20])},
		{"changed byte", base64.RawURLEncoding.EncodeToString(flipped)},
		{"truncated tag", base64.RawURLEncoding.EncodeToString(raw[:len(raw)-1])},
		{"padded base64", base64.URLEncoding.EncodeToString(flipped)},
		{"another key", otherCookies[0].Value},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, ok := m.Get(request(t, sent(good.Name, tt.value)))
			testutil.True(t, "Get refuses", !ok)
		})
	}

	_, ok := m.Get(request(t))
	testutil.True(t, "no cookie", !ok)
}

func TestFlowCookieIsNotASession(t *testing.T) {
	t.Parallel()

	m := manager(t, false)

	flow, err := session.NewFlow("/")
	testutil.NoError(t, err)

	rec := httptest.NewRecorder()
	testutil.NoError(t, m.SetFlow(rec, flow))

	flowCookie := rec.Result().Cookies()[0]
	testutil.Equal(t, "flow cookie name", flowCookie.Name, "wpfleet_flow")

	// The flow's sealed value under the session's name must not open, because
	// the purpose is sealed into it.
	_, ok := m.Get(request(t, sent("wpfleet_session", flowCookie.Value)))
	testutil.True(t, "flow value refused as a session", !ok)

	_, cookies := issue(t, m)

	_, ok = m.TakeFlow(httptest.NewRecorder(), request(t, sent("wpfleet_flow", cookies[0].Value)))
	testutil.True(t, "session value refused as a flow", !ok)
}

func TestSessionExpires(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		m := manager(t, true)
		_, cookies := issue(t, m)

		synctest.Sleep(maxAge - time.Minute)

		_, ok := m.Get(request(t, cookies...))
		testutil.True(t, "valid before expiry", ok)

		synctest.Sleep(time.Minute + time.Second)

		_, ok = m.Get(request(t, cookies...))
		testutil.True(t, "refused after expiry", !ok)
	})
}

func TestLoweredMaxAgeEndsOlderSessions(t *testing.T) {
	t.Parallel()

	_, cookies := issue(t, manager(t, true))

	shorter, err := session.NewManager(key(7), time.Hour, true)
	testutil.NoError(t, err)

	_, ok := shorter.Get(request(t, cookies...))
	testutil.True(t, "a session longer than the new max age is refused", !ok)
}

func TestSetRefuses(t *testing.T) {
	t.Parallel()

	m := manager(t, true)

	good, err := m.New(identity(), "")
	testutil.NoError(t, err)

	noSubject := good
	noSubject.Identity.Subject = ""

	noCSRF := good
	noCSRF.CSRF = ""

	expired := good
	expired.Expires = time.Now().Add(-time.Second)

	huge := good
	huge.Identity.Groups = make([]string, 400)

	for i := range huge.Identity.Groups {
		huge.Identity.Groups[i] = fmt.Sprintf("group-with-a-long-name-%03d", i)
	}

	for name, tt := range map[string]struct {
		s    session.Session
		want error
	}{
		"no subject": {noSubject, session.ErrInvalid},
		"no CSRF":    {noCSRF, session.ErrInvalid},
		"expired":    {expired, session.ErrInvalid},
		"too large":  {huge, session.ErrTooLarge},
	} {
		rec := httptest.NewRecorder()

		err := m.Set(rec, tt.s)
		testutil.ErrorIs(t, err, tt.want)
		testutil.Equal(t, name+": no cookie written", len(rec.Result().Cookies()), 0)
	}
}

func TestClear(t *testing.T) {
	t.Parallel()

	m := manager(t, true)
	rec := httptest.NewRecorder()
	m.Clear(rec)

	cookies := rec.Result().Cookies()
	testutil.Equal(t, "cookies", len(cookies), 1)
	testutil.Equal(t, "name", cookies[0].Name, "__Host-wpfleet_session")
	testutil.True(t, "MaxAge deletes", cookies[0].MaxAge < 0)
	testutil.Equal(t, "value", cookies[0].Value, "")
	testutil.True(t, "Secure", cookies[0].Secure)
}

func TestNew(t *testing.T) {
	t.Parallel()

	m := manager(t, true)

	a, err := m.New(identity(), "fa")
	testutil.NoError(t, err)

	b, err := m.New(identity(), "en")
	testutil.NoError(t, err)

	testutil.Equal(t, "CSRF length", len(a.CSRF), 43)
	testutil.True(t, "CSRF tokens differ", a.CSRF != b.CSRF)
	testutil.Equal(t, "Lang", a.Lang, "fa")
	testutil.True(t, "Expires within max age", !a.Expires.After(time.Now().Add(maxAge)))
	testutil.True(t, "Expires near max age", a.Expires.After(time.Now().Add(maxAge-2*time.Second)))
}

func TestCheckCSRF(t *testing.T) {
	t.Parallel()

	m := manager(t, true)

	s, err := m.New(identity(), "")
	testutil.NoError(t, err)

	form := func(token string) *http.Request {
		body := strings.NewReader(url.Values{"csrf": {token}}.Encode())
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, deployPath, body)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		return r
	}

	header := func(token string) *http.Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, deployPath, nil)
		r.Header.Set(session.CSRFHeader, token)

		return r
	}

	query := httptest.NewRequestWithContext(t.Context(), http.MethodPost, deployPath+"?csrf="+url.QueryEscape(s.CSRF), nil)

	wrongHeaderRightForm := form(s.CSRF)
	wrongHeaderRightForm.Header.Set(session.CSRFHeader, "wrong")

	noCSRF := s
	noCSRF.CSRF = ""

	tests := []struct {
		name string
		r    *http.Request
		s    session.Session
		want bool
	}{
		{"form field", form(s.CSRF), s, true},
		{"header", header(s.CSRF), s, true},
		{"wrong form field", form(s.CSRF[:42] + "x"), s, false},
		{"wrong header", header("x" + s.CSRF[1:]), s, false},
		{"shorter token", form(s.CSRF[:10]), s, false},
		{"longer token", form(s.CSRF + "x"), s, false},
		{"missing", form(""), s, false},
		{"query string is not accepted", query, s, false},
		{"a wrong header is not rescued by the form", wrongHeaderRightForm, s, false},
		{"session without a token", form(""), noCSRF, false},
	}

	for _, tt := range tests {
		testutil.Equal(t, tt.name, m.CheckCSRF(tt.r, tt.s), tt.want)
	}
}

func TestFlowRoundTrip(t *testing.T) {
	t.Parallel()

	m := manager(t, true)

	flow, err := session.NewFlow("/sites/staging/blog?tab=backups")
	testutil.NoError(t, err)

	flow.Lang = "fa"

	testutil.Equal(t, "verifier length", len(flow.Verifier), 43)
	testutil.True(t, "state, nonce and verifier differ", flow.State != flow.Nonce && flow.Nonce != flow.Verifier)

	rec := httptest.NewRecorder()
	testutil.NoError(t, m.SetFlow(rec, flow))

	cookies := rec.Result().Cookies()
	testutil.Equal(t, "flow cookie", cookies[0].Name, "__Host-wpfleet_flow")
	testutil.True(t, "HttpOnly", cookies[0].HttpOnly)
	testutil.Equal(t, "SameSite", cookies[0].SameSite, http.SameSiteLaxMode)
	testutil.True(t, "MaxAge about FlowTTL", cookies[0].MaxAge <= int(session.FlowTTL.Seconds()))

	take := httptest.NewRecorder()

	got, ok := m.TakeFlow(take, request(t, cookies...))
	testutil.True(t, "TakeFlow", ok)
	testutil.DeepEqual(t, "flow", got, flow)
	testutil.True(t, "state checks", got.CheckState(flow.State))

	cleared := take.Result().Cookies()
	testutil.Equal(t, "TakeFlow clears the cookie", len(cleared), 1)
	testutil.True(t, "cleared MaxAge", cleared[0].MaxAge < 0)
	testutil.Equal(t, "cleared name", cleared[0].Name, "__Host-wpfleet_flow")
}

func TestFlowExpires(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		m := manager(t, false)

		flow, err := session.NewFlow("/")
		testutil.NoError(t, err)

		rec := httptest.NewRecorder()
		testutil.NoError(t, m.SetFlow(rec, flow))

		synctest.Sleep(session.FlowTTL + time.Second)

		_, ok := m.TakeFlow(httptest.NewRecorder(), request(t, rec.Result().Cookies()...))
		testutil.True(t, "expired flow refused", !ok)
	})
}

func TestSetFlowRefuses(t *testing.T) {
	t.Parallel()

	m := manager(t, false)

	flow, err := session.NewFlow("/")
	testutil.NoError(t, err)

	for name, change := range map[string]func(*session.Flow){
		"no state":    func(f *session.Flow) { f.State = "" },
		"no nonce":    func(f *session.Flow) { f.Nonce = "" },
		"no verifier": func(f *session.Flow) { f.Verifier = "" },
		"expired":     func(f *session.Flow) { f.Expires = time.Now().Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bad := flow
			change(&bad)

			testutil.ErrorIs(t, m.SetFlow(httptest.NewRecorder(), bad), session.ErrInvalid)
		})
	}
}

func TestSetFlowSanitizesReturnTo(t *testing.T) {
	t.Parallel()

	m := manager(t, false)

	flow, err := session.NewFlow("/")
	testutil.NoError(t, err)

	flow.ReturnTo = "//evil.example.com/"

	rec := httptest.NewRecorder()
	testutil.NoError(t, m.SetFlow(rec, flow))

	got, ok := m.TakeFlow(httptest.NewRecorder(), request(t, rec.Result().Cookies()...))
	testutil.True(t, "TakeFlow", ok)
	testutil.Equal(t, "ReturnTo", got.ReturnTo, "/")
}

func TestTakeFlowWithoutCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	_, ok := manager(t, false).TakeFlow(rec, request(t))
	testutil.True(t, "no flow", !ok)
}

func TestCheckState(t *testing.T) {
	t.Parallel()

	flow, err := session.NewFlow("/")
	testutil.NoError(t, err)

	testutil.True(t, "same", flow.CheckState(flow.State))
	testutil.True(t, "other", !flow.CheckState(flow.Nonce))
	testutil.True(t, "empty", !flow.CheckState(""))

	var empty session.Flow

	testutil.True(t, "an empty flow matches nothing", !empty.CheckState(""))
}

func TestSafeReturnPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{"/", "/"},
		{sitePath, sitePath},
		{"/sites/staging/blog?tab=backups&x=1", "/sites/staging/blog?tab=backups&x=1"},
		{sitePath + "#history", sitePath},
		{"/%2F%2Fevil.example.com", "/%2F%2Fevil.example.com"},
		{"", "/"},
		{"sites/staging", "/"},
		{"https://evil.example.com/", "/"},
		{"http:evil.example.com", "/"},
		{"//evil.example.com", "/"},
		{"///evil.example.com", "/"},
		{"/\\evil.example.com", "/"},
		{"/sites\\..\\x", "/"},
		{"/\t/evil.example.com", "/"},
		{"/\n/evil.example.com", "/"},
		{"/sites/\x00", "/"},
		{"/sites/\x7f", "/"},
		{"javascript:alert(1)", "/"},
		{"/auth/login", "/"},
		{"/auth/callback?code=x", "/"},
		{"/auth", "/"},
		{"/authors", "/authors"},
		{"/" + strings.Repeat("a", 2048), "/"},
	}

	for _, tt := range tests {
		testutil.Equal(t, fmt.Sprintf("SafeReturnPath(%q)", tt.raw), session.SafeReturnPath(tt.raw), tt.want)
	}
}

func TestNewManagerRefuses(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 24, 31, 33, 64} {
		_, err := session.NewManager(make([]byte, size), maxAge, true)
		testutil.ErrorIs(t, err, session.ErrKeySize)
	}

	for _, age := range []time.Duration{0, -time.Hour} {
		_, err := session.NewManager(key(1), age, true)
		testutil.ErrorIs(t, err, session.ErrMaxAge)
	}
}

func TestDecodeKey(t *testing.T) {
	t.Parallel()

	want := make([]byte, session.KeySize)
	for i := range want {
		want[i] = byte(250 - i) // bytes that make + and / in standard base64
	}

	for name, encoded := range map[string]string{
		"standard":         base64.StdEncoding.EncodeToString(want),
		"standard raw":     base64.RawStdEncoding.EncodeToString(want),
		"URL-safe":         base64.URLEncoding.EncodeToString(want),
		"URL-safe raw":     base64.RawURLEncoding.EncodeToString(want),
		"trailing newline": base64.StdEncoding.EncodeToString(want) + "\n",
	} {
		got, err := session.DecodeKey(encoded)
		testutil.NoError(t, err)
		testutil.DeepEqual(t, name, got, want)
	}

	_, err := session.DecodeKey(base64.StdEncoding.EncodeToString(make([]byte, 16)))
	testutil.ErrorIs(t, err, session.ErrKeySize)

	_, err = session.DecodeKey("not base64 at all!")
	testutil.Error(t, err, "not base64")
}

func TestToken(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 100 {
		token, err := session.Token()
		testutil.NoError(t, err)
		testutil.Equal(t, "length", len(token), 43)
		testutil.True(t, "unique", !seen[token])

		seen[token] = true
	}
}
