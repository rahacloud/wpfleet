package admin_test

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rahacloud/wpfleet/internal/cmd/admin"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// plugin is the chart's must-use plugin.
const plugin = "../../../charts/wordpress/files/wpfleet-login.php"

// outcome is what the plugin did in the harness.
type outcome struct {
	Log     []string `json:"log"`
	Options []string `json:"options"`
}

// loggedIn reports whether the plugin logged user 7 in and redirected.
func (o outcome) loggedIn() bool {
	return slices.Contains(o.Log, "cookie:7") && slices.Contains(o.Log, "action:wp_login") &&
		slices.Contains(o.Log, "redirect:https://blog.example.com/wp-admin/")
}

// issued is a login that Issue stored through the fake wp-cli.
type issued struct {
	token, name, value string
}

// issue runs Issue at now, with the fake site's administrator being admin.
func issue(t *testing.T, now time.Time, adminID string) issued {
	t.Helper()

	answers := singleSite()
	answers["user list"] = adminID
	wp := newFakeWP(answers)

	login, err := admin.Issue(t.Context(), wp,
		admin.Request{Pod: blogPod, BaseURL: blogURL, User: "", Now: now}, seed())
	testutil.NoError(t, err)

	_, token, _ := strings.Cut(login.URL, "wpfleet-login=")
	c := wp.call(t, "option", "add")

	return issued{token: token, name: c[3], value: c[4]}
}

// runPlugin runs the plugin for a request with token, with one option stored.
func runPlugin(t *testing.T, php, token, name, value string) outcome {
	t.Helper()

	//nolint:gosec // the test runs the local php on its own harness
	out, err := exec.CommandContext(t.Context(), php, "testdata/harness.php", plugin, token, name, value).Output()
	testutil.NoError(t, err)

	var o outcome
	testutil.NoError(t, json.Unmarshal(out, &o))

	return o
}

// TestPlugin drives wpfleet-login.php with what Issue stores, through stubs of
// the WordPress functions it calls. It needs php on the PATH.
func TestPlugin(t *testing.T) {
	t.Parallel()

	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php is not installed")
	}

	now := time.Now()
	valid := issue(t, now, "7")

	o := runPlugin(t, php, valid.token, valid.name, valid.value)
	testutil.True(t, "a valid token logs in: "+strings.Join(o.Log, ","), o.loggedIn())
	testutil.Equal(t, "the option is deleted", len(o.Options), 0)

	other := issue(t, now, "7")
	o = runPlugin(t, php, strings.Repeat("A", 43), other.name, other.value)
	testutil.Equal(t, "another token does nothing", len(o.Log), 0)
	testutil.DeepEqual(t, "another token keeps the option", o.Options, []string{other.name})

	o = runPlugin(t, php, "not-a-token", other.name, other.value)
	testutil.Equal(t, "a malformed token does nothing", len(o.Log), 0)

	expired := issue(t, now.Add(-2*admin.TTL), "7")
	o = runPlugin(t, php, expired.token, expired.name, expired.value)
	testutil.Equal(t, "an expired token does nothing", len(o.Log), 0)
	testutil.Equal(t, "an expired token is deleted", len(o.Options), 0)

	gone := issue(t, now, "8")
	o = runPlugin(t, php, gone.token, gone.name, gone.value)
	testutil.Equal(t, "a deleted user does nothing", len(o.Log), 0)

	var tampered admin.Stored
	testutil.NoError(t, json.Unmarshal([]byte(valid.value), &tampered))
	tampered.Hash = strings.Repeat("0", 64)
	value, err := json.Marshal(tampered)
	testutil.NoError(t, err)
	o = runPlugin(t, php, valid.token, valid.name, string(value))
	testutil.Equal(t, "a hash that does not match does nothing", len(o.Log), 0)
}
