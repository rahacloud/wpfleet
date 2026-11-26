package admin_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/cmd/admin"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// The pod and URL of the site the Issue tests log in to.
const (
	blogPod = "blog-0"
	blogURL = "https://blog.example.com"
)

// tokenPattern is what wpfleet-login.php accepts.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// errWP is what the fake wp-cli fails with.
var errWP = errors.New("wp-cli failed")

// fakeWP is a wp-cli that records its calls and answers them from a table
// keyed by the first two arguments.
type fakeWP struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]string
	fail    string
}

func newFakeWP(answers map[string]string) *fakeWP {
	return &fakeWP{mu: sync.Mutex{}, calls: nil, answers: answers, fail: ""}
}

// Output answers args; the command named by fail fails.
func (f *fakeWP) Output(_ context.Context, pod string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, append([]string{pod}, args...))

	key := strings.Join(args[:min(2, len(args))], " ")
	if key == f.fail {
		return "", errWP
	}

	return f.answers[key], nil
}

// call returns the first call whose arguments start with prefix.
func (f *fakeWP) call(t *testing.T, prefix ...string) []string {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	for _, c := range f.calls {
		if len(c) > len(prefix) && slices.Equal(c[1:len(prefix)+1], prefix) {
			return c
		}
	}

	t.Fatalf("no wp %s in %v", strings.Join(prefix, " "), f.calls)

	return nil
}

// stored decodes the option that the fake was asked to add.
func (f *fakeWP) stored(t *testing.T) (string, admin.Stored) {
	t.Helper()

	c := f.call(t, "option", "add")

	var s admin.Stored
	testutil.NoError(t, json.Unmarshal([]byte(c[4]), &s))

	return c[3], s
}

// clockKey is the call that reads the site's clock.
const clockKey = "eval echo time();"

// siteClock is the site's time in the fake: 1_800_000_000.
const siteClock = "1800000000"

// singleSite answers like a site with the administrator 7 whose clock reads
// siteClock.
func singleSite() map[string]string {
	return map[string]string{
		"eval echo is_multisite() ? 'yes' : 'no';": "no", "user list": "7", "user get": "12", clockKey: siteClock,
	}
}

func seed() *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0xfb}, admin.TokenBytes))
}

func TestNewToken(t *testing.T) {
	t.Parallel()

	token, err := admin.NewToken(seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "seeded token", token, strings.Repeat("-_v7", 10)+"-_s")
	testutil.True(t, "the plugin's pattern", tokenPattern.MatchString(token))

	a, err := admin.NewToken(rand.Reader)
	testutil.NoError(t, err)
	b, err := admin.NewToken(rand.Reader)
	testutil.NoError(t, err)
	testutil.True(t, "random tokens differ", a != b && tokenPattern.MatchString(a))

	_, err = admin.NewToken(bytes.NewReader(make([]byte, admin.TokenBytes-1)))
	testutil.Error(t, err, "short randomness")

	testutil.Equal(t, "option name", admin.OptionName("abc"),
		"_wpfleet_login_ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
}

func TestIssueSingleSite(t *testing.T) {
	t.Parallel()

	wp := newFakeWP(singleSite())

	login, err := admin.Issue(t.Context(), wp, admin.Request{Pod: blogPod, BaseURL: blogURL, User: ""}, seed())
	testutil.NoError(t, err)

	token, _ := admin.NewToken(seed())
	testutil.Equal(t, "url", login.URL, "https://blog.example.com/?wpfleet-login="+token)
	testutil.Equal(t, "user", login.UserID, 7)
	testutil.Equal(t, "expires by the site's clock", login.Expires, time.Unix(1_800_000_060, 0))
	testutil.DeepEqual(t, "clock read on the site", wp.call(t, "eval", "echo time();")[3:],
		[]string{"--url=" + blogURL + "/"})

	list := wp.call(t, "user", "list")
	testutil.DeepEqual(t, "user list", list[3:], []string{"--role=administrator", "--field=ID", "--number=1",
		"--orderby=ID", "--order=ASC", "--url=" + blogURL + "/"})

	name, stored := wp.stored(t)
	testutil.Equal(t, "option name", name, admin.OptionName(token))
	testutil.Equal(t, "hash", stored.Hash, admin.TokenHash(token))
	testutil.Equal(t, "stored user", stored.User, 7)
	testutil.Equal(t, "stored expiry", stored.Expires, login.Expires.Unix())
	testutil.DeepEqual(t, "option flags", wp.call(t, "option", "add")[5:],
		[]string{"--format=json", "--autoload=no", "--url=" + blogURL + "/"})

	for _, c := range wp.calls {
		testutil.Equal(t, "pod", c[0], "blog-0")
		testutil.NotContains(t, "wp-cli arguments", strings.Join(c, " "), token)
	}
}

func TestIssueUsers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	req := admin.Request{Pod: blogPod, BaseURL: blogURL, User: "editor"}

	wp := newFakeWP(singleSite())
	login, err := admin.Issue(ctx, wp, req, seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "named user", login.UserID, 12)
	testutil.Equal(t, "user get login", wp.call(t, "user", "get")[3], "editor")

	// A multisite logs in its first super admin, who may hold no role on the
	// main site.
	multi := singleSite()
	multi["eval echo is_multisite() ? 'yes' : 'no';"] = "yes"
	multi["super-admin list"] = "network-admin\nother"
	req.User = ""
	wp = newFakeWP(multi)
	login, err = admin.Issue(ctx, wp, req, seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "super admin", login.UserID, 12)
	testutil.Equal(t, "super admin login", wp.call(t, "user", "get")[3], "network-admin")

	// A multisite without super admins falls back to an administrator.
	multi["super-admin list"] = ""
	login, err = admin.Issue(ctx, newFakeWP(multi), req, seed())
	testutil.NoError(t, err)
	testutil.Equal(t, "fallback administrator", login.UserID, 7)
}

func TestIssueErrors(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	req := admin.Request{Pod: blogPod, BaseURL: blogURL, User: ""}

	noAdmin := singleSite()
	noAdmin["user list"] = ""
	_, err := admin.Issue(ctx, newFakeWP(noAdmin), req, seed())
	testutil.ErrorIs(t, err, admin.ErrNoAdmin)

	notNumber := singleSite()
	notNumber["user list"] = "Error: nope"
	_, err = admin.Issue(ctx, newFakeWP(notNumber), req, seed())
	testutil.ErrorIs(t, err, admin.ErrBadUser)

	flagLike := req
	flagLike.User = "--role=subscriber"
	wp := newFakeWP(singleSite())
	_, err = admin.Issue(ctx, wp, flagLike, seed())
	testutil.ErrorIs(t, err, admin.ErrBadUser)
	testutil.Equal(t, "no wp-cli call", len(wp.calls), 0)

	badClock := singleSite()
	badClock[clockKey] = "Error: no"
	_, err = admin.Issue(ctx, newFakeWP(badClock), req, seed())
	testutil.ErrorIs(t, err, admin.ErrBadClock)

	for _, fail := range []string{"eval echo is_multisite() ? 'yes' : 'no';", "user list", clockKey, "option add"} {
		wp := newFakeWP(singleSite())
		wp.fail = fail
		_, err = admin.Issue(ctx, wp, req, seed())
		testutil.ErrorIs(t, err, errWP)
	}

	_, err = admin.Issue(ctx, newFakeWP(singleSite()), req, bytes.NewReader(nil))
	testutil.Error(t, err, "no randomness")
}
