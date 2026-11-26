package assets_test

import (
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/assets"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// The hosts the tests move between.
const (
	oldHost = "blog-staging.example.com"
	newHost = "blog-dev.example.com"
)

// The WPML caches rehost-extra.php deletes when they hold the old host.
const (
	wpmlCache = "wpml_language_switcher_template_objects"
	wcmlCache = "wcml_currency_switcher_template_objects"
)

// item is a BeTheme page builder element: fields, and elements nested in it.
type item struct {
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields"`
	Items  []item            `json:"items"`
}

// metaIn is a postmeta row of the harness's input: items to store as BeTheme
// does, or a raw value to store as is.
type metaIn struct {
	MetaID int     `json:"meta_id"`
	Items  []item  `json:"items,omitempty"`
	Raw    *string `json:"raw,omitempty"`
}

// blogIn is one blog of the harness's input.
type blogIn struct {
	Postmeta []metaIn          `json:"postmeta"`
	Options  map[string]string `json:"options"`
}

// state is the harness's input.
type state struct {
	Multisite bool              `json:"multisite"`
	Blogs     map[string]blogIn `json:"blogs"`
}

// row is a postmeta row after the run: the stored value and its items when
// it decodes to an array.
type row struct {
	Value string `json:"value"`
	Items []item `json:"items"`
}

// blogOut is one blog after the run.
type blogOut struct {
	Postmeta map[string]row    `json:"postmeta"`
	Options  map[string]string `json:"options"`
}

// result is what the harness prints.
type result struct {
	Log     []string           `json:"log"`
	Flushed bool               `json:"flushed"`
	Blogs   map[string]blogOut `json:"blogs"`
}

// page is a page with host in a nested element, as BeTheme stores it.
func page(host string) []item {
	return []item{{
		Type:   "section",
		Fields: map[string]string{"title": "Home", "bg": "https://" + host + "/wp-content/uploads/bg.jpg"},
		Items: []item{{
			Type:   "column",
			Fields: map[string]string{"content": `<a href="https://` + host + `/shop">Shop</a>`},
			Items:  []item{},
		}},
	}}
}

// notArray is a stored value that holds the old host in a serialized string,
// not an array.
func notArray() string {
	return base64.StdEncoding.EncodeToString([]byte(`s:24:"` + oldHost + `";`))
}

// blogState is one blog with a page holding the old host, a page without it,
// a serialized string with the old host (not an array), and two WPML caches,
// one of them holding the old host.
func blogState() blogIn {
	raw := notArray()

	return blogIn{
		Postmeta: []metaIn{
			{MetaID: 1, Items: page(oldHost), Raw: nil},
			{MetaID: 2, Items: page("other.example.org"), Raw: nil},
			{MetaID: 3, Items: nil, Raw: &raw},
		},
		Options: map[string]string{
			wpmlCache: `O:8:"stdClass":1:{s:3:"url";s:33:"https://` + oldHost + `/";}`,
			wcmlCache: `a:0:{}`,
		},
	}
}

// requirePHP returns the php binary, or skips the test without one.
func requirePHP(t *testing.T) string {
	t.Helper()

	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php is not installed")
	}

	return php
}

// runRehost runs rehost-extra.php in the PHP harness on st with args.
func runRehost(t *testing.T, st state, args ...string) result {
	t.Helper()

	php := requirePHP(t)
	dir := t.TempDir()
	script := testutil.WriteFile(t, dir, assets.RehostExtraName, string(assets.RehostExtra))

	data, err := json.Marshal(st)
	testutil.NoError(t, err)

	input := testutil.WriteFile(t, dir, "state.json", string(data))

	//nolint:gosec // the test runs the local php on its own harness
	out, err := exec.CommandContext(t.Context(), php,
		append([]string{"testdata/harness.php", script, input}, args...)...).Output()
	testutil.NoError(t, err)

	var r result
	testutil.NoError(t, json.Unmarshal(out, &r))

	return r
}

func TestRehostExtraParses(t *testing.T) {
	t.Parallel()

	php := requirePHP(t)
	script := testutil.WriteFile(t, t.TempDir(), assets.RehostExtraName, string(assets.RehostExtra))

	//nolint:gosec // the test lints its own file with the local php
	out, err := exec.CommandContext(t.Context(), php, "-l", script).CombinedOutput()
	testutil.NoError(t, err)
	testutil.Contains(t, "php -l", string(out), "No syntax errors")
}

func TestRehostExtra(t *testing.T) {
	t.Parallel()

	r := runRehost(t, state{Multisite: false, Blogs: map[string]blogIn{"1": blogState()}}, oldHost, newHost)
	blog := r.Blogs["1"]

	testutil.DeepEqual(t, "nested strings move to the new host", blog.Postmeta["1"].Items, page(newHost))
	testutil.DeepEqual(t, "a page without the old host", blog.Postmeta["2"].Items, page("other.example.org"))
	testutil.Equal(t, "a value that is not an array is left alone", blog.Postmeta["3"].Value, notArray())

	_, kept := blog.Options[wpmlCache]
	testutil.True(t, "the WPML cache with the old host is deleted", !kept)
	testutil.Equal(t, "a WPML cache without it stays", blog.Options[wcmlCache], "a:0:{}")

	testutil.DeepEqual(t, "log", r.Log, []string{
		"Warning: blog 1 meta 3: not a serialized array, left alone",
		"blog 1: 1 BeTheme pages, 1 WPML caches",
	})
	testutil.True(t, "the cache is flushed", r.Flushed)
}

func TestRehostExtraDryRun(t *testing.T) {
	t.Parallel()

	before := blogState()
	r := runRehost(t, state{Multisite: false, Blogs: map[string]blogIn{"1": before}}, oldHost, newHost, "dry-run")
	blog := r.Blogs["1"]

	testutil.DeepEqual(t, "nothing rewritten", blog.Postmeta["1"].Items, page(oldHost))
	testutil.DeepEqual(t, "nothing deleted", blog.Options, before.Options)
	testutil.Equal(t, "log", r.Log[len(r.Log)-1], "blog 1: 1 BeTheme pages, 1 WPML caches (dry run)")
}

func TestRehostExtraMultisite(t *testing.T) {
	t.Parallel()

	blogs := map[string]blogIn{}

	for id := range 3 {
		blogs[strconv.Itoa(id+1)] = blogState()
	}

	r := runRehost(t, state{Multisite: true, Blogs: blogs}, oldHost, newHost)
	log := strings.Join(r.Log, "\n")

	for id := range blogs {
		testutil.DeepEqual(t, "blog "+id, r.Blogs[id].Postmeta["1"].Items, page(newHost))
		testutil.Contains(t, "log", log, "blog "+id+": 1 BeTheme pages, 1 WPML caches")
	}
}
