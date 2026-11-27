package access_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	dev     = "development"
	staging = "staging"
	prod    = "production"
	blog    = "blog"
	shop    = "shop"
)

func rule(group string, envs, sites []string, allow ...access.Permission) access.Rule {
	return access.Rule{Group: group, Environments: envs, Sites: sites, Allow: allow}
}

func list(names ...string) []string { return names }

// designPolicy is the access section of the example in docs/design.md, plus
// one group per permission so each row of the permission table has a rule of
// its own.
func designPolicy() access.Policy {
	return access.Policy{Rules: []access.Rule{
		rule("platform", list("*"), list("*"), access.View, access.Deploy, access.Login, access.Backup, access.Restore),
		rule("shop-team", list(dev, staging), list(shop), access.View, access.Login),
		rule("viewers", list(staging), list(blog), access.View),
		rule("deployers", list(staging), list(blog), access.Deploy),
		rule("editors", list(staging), list(blog), access.Login),
		rule("backups", list(staging), list(blog), access.Backup),
		rule("restorers", list(staging), list(blog), access.Restore),
	}}
}

// allowsCase is one decision of TestAllows.
type allowsCase struct {
	name   string
	groups []string
	env    string
	site   string
	perm   access.Permission
	want   bool
}

// allowsCases are the decisions TestAllows checks against designPolicy.
func allowsCases() []allowsCase {
	return []allowsCase{
		// One case per row of the permission table: the permission itself, and
		// view, which it includes.
		{"view allows view", list("viewers"), staging, blog, access.View, true},
		{"view does not allow deploy", list("viewers"), staging, blog, access.Deploy, false},
		{"view does not allow login", list("viewers"), staging, blog, access.Login, false},
		{"view does not allow backup", list("viewers"), staging, blog, access.Backup, false},
		{"view does not allow restore", list("viewers"), staging, blog, access.Restore, false},
		{"deploy allows deploy", list("deployers"), staging, blog, access.Deploy, true},
		{"deploy includes view", list("deployers"), staging, blog, access.View, true},
		{"deploy does not allow login", list("deployers"), staging, blog, access.Login, false},
		{"login allows login", list("editors"), staging, blog, access.Login, true},
		{"login includes view", list("editors"), staging, blog, access.View, true},
		{"login does not allow deploy", list("editors"), staging, blog, access.Deploy, false},
		{"backup allows backup", list("backups"), staging, blog, access.Backup, true},
		{"backup includes view", list("backups"), staging, blog, access.View, true},
		{"backup does not allow restore", list("backups"), staging, blog, access.Restore, false},
		{"restore allows restore", list("restorers"), staging, blog, access.Restore, true},
		{"restore includes view", list("restorers"), staging, blog, access.View, true},
		{"restore does not allow backup", list("restorers"), staging, blog, access.Backup, false},

		// The design's example rules.
		{"platform deploys anywhere", list("platform"), prod, blog, access.Deploy, true},
		{"platform restores anywhere", list("platform"), dev, shop, access.Restore, true},
		{"shop-team logs in to shop in staging", list("shop-team"), staging, shop, access.Login, true},
		{"shop-team views shop in development", list("shop-team"), dev, shop, access.View, true},
		{"shop-team cannot touch production", list("shop-team"), prod, shop, access.View, false},
		{"shop-team cannot see blog", list("shop-team"), staging, blog, access.View, false},
		{"shop-team cannot deploy shop", list("shop-team"), staging, shop, access.Deploy, false},

		// Deny by default.
		{"no groups", nil, staging, blog, access.View, false},
		{"empty group list", list(), staging, blog, access.View, false},
		{"unknown group", list("marketing"), staging, blog, access.View, false},
		{"empty group name", list(""), staging, blog, access.View, false},
		{"other environment", list("viewers"), prod, blog, access.View, false},
		{"other site", list("viewers"), staging, shop, access.View, false},
		{"empty environment", list("platform"), "", blog, access.View, false},
		{"empty site", list("platform"), staging, "", access.View, false},
		{"unknown permission", list("platform"), staging, blog, access.Permission("admin"), false},
		{"empty permission", list("platform"), staging, blog, access.Permission(""), false},

		// Names are exact.
		{"group names are case sensitive", list("Platform"), staging, blog, access.View, false},
		{"site names are case sensitive", list("viewers"), staging, "Blog", access.View, false},
		{"a group is not a prefix", list("view"), staging, blog, access.View, false},

		// Union over groups.
		{"union: second group grants it", list("viewers", "editors"), staging, blog, access.Login, true},
		{"union: order does not matter", list("editors", "marketing", "viewers"), staging, blog, access.Login, true},
		{"union never widens a rule's scope", list("viewers", "shop-team"), staging, blog, access.Login, false},
	}
}

func TestAllows(t *testing.T) {
	t.Parallel()

	policy := designPolicy()

	for _, tt := range allowsCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := policy.Allows(tt.groups, tt.env, tt.site, tt.perm)
			testutil.Equal(t, "Allows", got, tt.want)
		})
	}
}

func TestZeroPolicyDeniesEverything(t *testing.T) {
	t.Parallel()

	var policy access.Policy

	for _, perm := range access.All() {
		testutil.True(t, "zero policy denies "+string(perm), !policy.Allows(list("platform"), staging, blog, perm))
	}

	testutil.True(t, "zero policy has no permissions", policy.Permissions(list("platform"), staging, blog) == nil)
	testutil.True(t, "zero policy shows nothing", !policy.Any(list("platform")))
}

func TestPartialGlobIsNotAPattern(t *testing.T) {
	t.Parallel()

	// Validate refuses this rule; if a caller skipped Validate, the name must
	// still not act as a pattern.
	policy := access.Policy{Rules: []access.Rule{
		rule("team", list("*"), list("shop-*"), access.View),
		rule("team", list("stag*"), list("*"), access.View),
	}}

	testutil.True(t, "shop-* does not match shop-de", !policy.Allows(list("team"), staging, "shop-de", access.View))
	testutil.True(t, "stag* does not match staging", !policy.Allows(list("team"), staging, blog, access.View))
	testutil.True(t, "shop-* matches only itself", policy.Allows(list("team"), staging, "shop-*", access.View))
}

func TestRuleWithOnlyUnknownPermissionsGrantsNoView(t *testing.T) {
	t.Parallel()

	policy := access.Policy{Rules: []access.Rule{rule("team", list("*"), list("*"), "admin")}}

	testutil.True(t, "no view from an unknown permission", !policy.Allows(list("team"), staging, blog, access.View))
	testutil.True(t, "Any", !policy.Any(list("team")))
}

func TestPromoteChecks(t *testing.T) {
	t.Parallel()

	// promote checks deploy on the target and view on the source.
	policy := access.Policy{Rules: []access.Rule{
		rule("publishers", list(prod), list(blog), access.Deploy),
		rule("publishers", list(staging), list(blog), access.View),
	}}
	groups := list("publishers")

	testutil.True(t, "deploy on target", policy.Allows(groups, prod, blog, access.Deploy))
	testutil.True(t, "view on source", policy.Allows(groups, staging, blog, access.View))
	testutil.True(t, "no deploy on source", !policy.Allows(groups, staging, blog, access.Deploy))
}

func TestPermissions(t *testing.T) {
	t.Parallel()

	policy := designPolicy()

	tests := []struct {
		name   string
		groups []string
		env    string
		site   string
		want   []access.Permission
	}{
		{"everything", list("platform"), prod, shop, access.All()},
		{"view and login", list("shop-team"), staging, shop, []access.Permission{access.View, access.Login}},
		{"login implies view", list("editors"), staging, blog, []access.Permission{access.View, access.Login}},
		{
			"union in canonical order", list("restorers", "deployers"), staging, blog,
			[]access.Permission{access.View, access.Deploy, access.Restore},
		},
		{"nothing", list("shop-team"), prod, shop, nil},
		{"no groups", nil, prod, shop, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testutil.DeepEqual(t, "Permissions", policy.Permissions(tt.groups, tt.env, tt.site), tt.want)
		})
	}
}

func TestAny(t *testing.T) {
	t.Parallel()

	policy := designPolicy()

	testutil.True(t, "shop-team sees something", policy.Any(list("marketing", "shop-team")))
	testutil.True(t, "marketing sees nothing", !policy.Any(list("marketing")))
	testutil.True(t, "no groups see nothing", !policy.Any(nil))
}

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := rule("team", list(staging), list(blog), access.View)

	tests := []struct {
		name string
		rule access.Rule
		want string // empty: valid
	}{
		{"valid", valid, ""},
		{"wildcards", rule("team", list("*"), list("*"), access.All()...), ""},
		{"wildcard beside names", rule("team", list("*", staging), list(blog, "*"), access.Login), ""},
		{"empty group", rule("", list(staging), list(blog), access.View), "group is empty"},
		{"blank group", rule("  ", list(staging), list(blog), access.View), "group is empty"},
		{"padded group", rule(" team", list(staging), list(blog), access.View), "leading or trailing spaces"},
		{"wildcard group", rule("*", list(staging), list(blog), access.View), "group cannot be a pattern"},
		{"no environments", rule("team", nil, list(blog), access.View), "environments is empty"},
		{"no sites", rule("team", list(staging), list(), access.View), "sites is empty"},
		{"empty site name", rule("team", list(staging), list(""), access.View), "sites has an empty name"},
		{"empty environment name", rule("team", list(" "), list(blog), access.View), "environments has an empty name"},
		{"padded site name", rule("team", list(staging), list("blog "), access.View), "leading or trailing spaces"},
		{"partial glob site", rule("team", list(staging), list("shop-*"), access.View), `"shop-*" is not allowed`},
		{"partial glob environment", rule("team", list("stag*"), list(blog), access.View), `"stag*" is not allowed`},
		{"double wildcard", rule("team", list("**"), list(blog), access.View), `"**" is not allowed`},
		{"no permissions", rule("team", list(staging), list(blog)), "allow is empty"},
		{"unknown permission", rule("team", list(staging), list(blog), access.View, "admin"), `unknown permission "admin"`},
		{"case of permissions", rule("team", list(staging), list(blog), "View"), `unknown permission "View"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := access.Policy{Rules: []access.Rule{valid, tt.rule}}.Validate()
			if tt.want == "" {
				testutil.NoError(t, err)

				return
			}

			testutil.ErrorIs(t, err, access.ErrInvalidRule)
			testutil.Contains(t, "error", err.Error(), tt.want, "rule 2")
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	t.Parallel()

	err := access.Policy{Rules: []access.Rule{
		rule("", nil, list("a*"), "admin"),
		rule("team", list(staging), nil),
	}}.Validate()

	testutil.ErrorIs(t, err, access.ErrInvalidRule)

	var joined interface{ Unwrap() []error }

	testutil.True(t, "joined error", errors.As(err, &joined))
	testutil.Equal(t, "problems", len(joined.Unwrap()), 6)
	testutil.True(t, "names the second rule", strings.Contains(err.Error(), `rule 2 (group "team")`))
}

func TestZeroPolicyIsValid(t *testing.T) {
	t.Parallel()

	var zero access.Policy

	testutil.NoError(t, zero.Validate())
}

func TestKnown(t *testing.T) {
	t.Parallel()

	for _, perm := range access.All() {
		testutil.True(t, string(perm), perm.Known())
	}

	testutil.True(t, "unknown", !access.Permission("admin").Known())
}
