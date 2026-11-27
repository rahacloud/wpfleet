package ops_test

import (
	"bytes"
	"crypto/rand"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/ops/opstest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// The pod and URL of the site the IssueLogin tests log in to.
const (
	blogPod = "blog-0"
	blogURL = "https://blog.example.com"
)

// tokenPattern is what wpfleet-login.php accepts.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestNewToken(t *testing.T) {
	t.Parallel()

	token, err := ops.NewLoginToken(opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "seeded token", token, strings.Repeat("-_v7", 10)+"-_s")
	testutil.True(t, "the plugin's pattern", tokenPattern.MatchString(token))

	a, err := ops.NewLoginToken(rand.Reader)
	testutil.NoError(t, err)
	b, err := ops.NewLoginToken(rand.Reader)
	testutil.NoError(t, err)
	testutil.True(t, "random tokens differ", a != b && tokenPattern.MatchString(a))

	_, err = ops.NewLoginToken(bytes.NewReader(make([]byte, ops.LoginTokenBytes-1)))
	testutil.Error(t, err, "short randomness")

	testutil.Equal(t, "option name", ops.LoginOptionName("abc"),
		"_wpfleet_login_ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
}

func TestIssueLoginSingleSite(t *testing.T) {
	t.Parallel()

	wp := opstest.NewWP(opstest.SingleSite())

	req := ops.LoginRequest{Pod: blogPod, BaseURL: blogURL, User: "", MatchEmail: ""}

	login, err := ops.IssueLogin(t.Context(), wp, req, opstest.Seed())
	testutil.NoError(t, err)

	token, _ := ops.NewLoginToken(opstest.Seed())
	testutil.Equal(t, "url", login.URL, "https://blog.example.com/?wpfleet-login="+token)
	testutil.Equal(t, "user", login.UserID, 7)
	testutil.Equal(t, "expires by the site's clock", login.Expires, time.Unix(1_800_000_060, 0))
	testutil.DeepEqual(t, "clock read on the site", wp.Call(t, "eval", "echo time();")[3:],
		[]string{"--url=" + blogURL + "/"})

	list := wp.Call(t, "user", "list")
	testutil.DeepEqual(t, "user list", list[3:], []string{"--role=administrator", "--field=ID", "--number=1",
		"--orderby=ID", "--order=ASC", "--url=" + blogURL + "/"})

	name, stored := wp.Stored(t)
	testutil.Equal(t, "option name", name, ops.LoginOptionName(token))
	testutil.Equal(t, "hash", stored.Hash, ops.LoginTokenHash(token))
	testutil.Equal(t, "stored user", stored.User, 7)
	testutil.Equal(t, "stored expiry", stored.Expires, login.Expires.Unix())
	testutil.DeepEqual(t, "option flags", wp.Call(t, "option", "add")[5:],
		[]string{"--format=json", "--autoload=no", "--url=" + blogURL + "/"})

	for _, c := range wp.Calls() {
		testutil.Equal(t, "pod", c[0], "blog-0")
		testutil.NotContains(t, "wp-cli arguments", strings.Join(c, " "), token)
	}
}

func TestIssueLoginUsers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	req := ops.LoginRequest{Pod: blogPod, BaseURL: blogURL, User: "editor", MatchEmail: ""}

	wp := opstest.NewWP(opstest.SingleSite())
	login, err := ops.IssueLogin(ctx, wp, req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "named user", login.UserID, 12)
	testutil.Equal(t, "user get login", wp.Call(t, "user", "get")[3], "editor")

	// A multisite logs in its first super admin, who may hold no role on the
	// main site.
	multi := opstest.SingleSite()
	multi[opstest.MultisiteKey] = "yes"
	multi["super-admin list"] = "network-admin\nother"
	req.User = ""
	wp = opstest.NewWP(multi)
	login, err = ops.IssueLogin(ctx, wp, req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "super admin", login.UserID, 12)
	testutil.Equal(t, "super admin login", wp.Call(t, "user", "get")[3], "network-admin")

	// A multisite without super admins falls back to an administrator.
	multi["super-admin list"] = ""
	login, err = ops.IssueLogin(ctx, opstest.NewWP(multi), req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "fallback administrator", login.UserID, 7)
}

func TestIssueLoginErrors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	req := ops.LoginRequest{Pod: blogPod, BaseURL: blogURL, User: "", MatchEmail: ""}

	noAdmin := opstest.SingleSite()
	noAdmin["user list"] = ""
	_, err := ops.IssueLogin(ctx, opstest.NewWP(noAdmin), req, opstest.Seed())
	testutil.ErrorIs(t, err, ops.ErrNoAdmin)

	notNumber := opstest.SingleSite()
	notNumber["user list"] = "Error: nope"
	_, err = ops.IssueLogin(ctx, opstest.NewWP(notNumber), req, opstest.Seed())
	testutil.ErrorIs(t, err, ops.ErrBadUser)

	flagLike := req
	flagLike.User = "--role=subscriber"
	wp := opstest.NewWP(opstest.SingleSite())
	_, err = ops.IssueLogin(ctx, wp, flagLike, opstest.Seed())
	testutil.ErrorIs(t, err, ops.ErrBadUser)
	testutil.Equal(t, "no wp-cli call", len(wp.Calls()), 0)

	badClock := opstest.SingleSite()
	badClock[opstest.ClockKey] = "Error: no"
	_, err = ops.IssueLogin(ctx, opstest.NewWP(badClock), req, opstest.Seed())
	testutil.ErrorIs(t, err, ops.ErrBadClock)

	for _, fail := range []string{opstest.MultisiteKey, "user list", opstest.ClockKey, "option add"} {
		wp := opstest.NewWP(opstest.SingleSite())
		wp.FailOn(fail)
		_, err = ops.IssueLogin(ctx, wp, req, opstest.Seed())
		testutil.ErrorIs(t, err, opstest.ErrWP)
	}

	_, err = ops.IssueLogin(ctx, opstest.NewWP(opstest.SingleSite()), req, bytes.NewReader(nil))
	testutil.Error(t, err, "no randomness")
}

func TestIssueLoginMatchEmail(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	req := ops.LoginRequest{Pod: blogPod, BaseURL: blogURL, User: "", MatchEmail: "editor@example.com"}
	isAdmin := `$u = get_userdata(12); echo ($u && (in_array("administrator", (array) $u->roles, true)` +
		` || is_super_admin(12))) ? "yes" : "no";`

	answers := opstest.SingleSite()
	answers["eval "+isAdmin] = "yes"
	wp := opstest.NewWP(answers)

	login, err := ops.IssueLogin(ctx, wp, req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "the administrator with the email", login.UserID, 12)
	testutil.True(t, "matched", login.MatchedEmail)
	testutil.Equal(t, "looked up by email", wp.Call(t, "user", "get")[3], "editor@example.com")

	// The user with the email is not an administrator: the first one is.
	answers["eval "+isAdmin] = "no"
	login, err = ops.IssueLogin(ctx, opstest.NewWP(answers), req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "fallback administrator", login.UserID, 7)
	testutil.True(t, "not matched", !login.MatchedEmail)

	// No user has the email.
	wp = opstest.NewWP(opstest.SingleSite())
	wp.FailOn("user get")
	login, err = ops.IssueLogin(ctx, wp, req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "no such user", login.UserID, 7)

	// An explicit user wins over the email, and a flag-like email is never
	// passed to wp-cli.
	req.User = "editor"
	login, err = ops.IssueLogin(ctx, opstest.NewWP(opstest.SingleSite()), req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.True(t, "the named user is not a match", login.UserID == 12 && !login.MatchedEmail)

	req.User, req.MatchEmail = "", "--role=x@example.com"
	wp = opstest.NewWP(opstest.SingleSite())
	login, err = ops.IssueLogin(ctx, wp, req, opstest.Seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "flag-like email ignored", login.UserID, 7)

	for _, c := range wp.Calls() {
		testutil.NotContains(t, "wp-cli arguments", strings.Join(c, " "), "--role=x")
	}
}
