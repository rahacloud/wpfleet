// Package access decides what a signed-in person may do with a site of the
// fleet. Access is denied unless a rule allows it: a rule names a group, the
// environments and sites it covers and the permissions it grants, and a
// person's permissions are the union of the rules that match one of their
// groups. The package is pure; the server hands it the groups from the
// session and the rules from wpfleet.yaml.
package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Permission is one thing a person may do with a site.
type Permission string

const (
	// View shows the site in lists, its details, its diff, its history and its
	// backups. Every other permission includes it.
	View Permission = "view"
	// Deploy allows promote into the environment and deploy there.
	Deploy Permission = "deploy"
	// Login allows the one-time admin login.
	Login Permission = "login"
	// Backup allows backup run.
	Backup Permission = "backup"
	// Restore allows backup restore, which overwrites the site's database and,
	// if asked, its uploads.
	Restore Permission = "restore"
)

// Wildcard matches every environment or every site. It stands for a whole
// name only: "shop-*" is not a pattern, and Validate refuses it.
const Wildcard = "*"

// ErrInvalidRule is wrapped by every error of Policy.Validate.
var ErrInvalidRule = errors.New("invalid access rule")

// All lists every known permission in the order the UI shows them.
func All() []Permission {
	return []Permission{View, Deploy, Login, Backup, Restore}
}

// Known reports whether p is one of the permissions above.
func (p Permission) Known() bool {
	return slices.Contains(All(), p)
}

// Identity is the signed-in person as the identity provider describes them.
// Groups is the only part access decisions read; the rest names the person in
// the audit log and in the admin login.
type Identity struct {
	// Subject is the provider's stable identifier of the person (sub).
	Subject string `json:"sub"`
	// Email is the person's email, empty when the provider gave none or said
	// it is not verified.
	Email string `json:"email,omitempty"`
	// Name is the person's display name.
	Name string `json:"name,omitempty"`
	// Groups are the groups from the configured groups claim.
	Groups []string `json:"groups,omitempty"`
}

// Rule grants permissions to the members of one group on the sites it covers.
type Rule struct {
	// Group is the group name exactly as the groups claim carries it.
	Group string `json:"group" koanf:"group"`
	// Environments are environment names, or Wildcard for all of them.
	Environments []string `json:"environments" koanf:"environments"`
	// Sites are site names, or Wildcard for all of them.
	Sites []string `json:"sites" koanf:"sites"`
	// Allow are the permissions the rule grants.
	Allow []Permission `json:"allow" koanf:"allow"`
}

// Policy is the ordered list of rules of wpfleet.yaml. Its zero value denies
// everything.
type Policy struct {
	// Rules are the rules; their order does not change any decision.
	Rules []Rule `json:"rules"`
}

// Validate refuses a rule that cannot mean what its author wanted: one with no
// group, no environment, no site or no permission, an unknown permission, an
// empty name, or a "*" that is part of a name instead of the whole name. It
// returns every problem it finds, each wrapping ErrInvalidRule.
func (p Policy) Validate() error {
	var errs []error

	for i, rule := range p.Rules {
		for _, problem := range rule.problems() {
			errs = append(errs, fmt.Errorf("%w %d (group %q): %s", ErrInvalidRule, i+1, rule.Group, problem))
		}
	}

	return errors.Join(errs...)
}

// problems describes what is wrong with one rule.
func (r Rule) problems() []string {
	var out []string

	switch {
	case strings.TrimSpace(r.Group) == "":
		out = append(out, "group is empty")
	case r.Group != strings.TrimSpace(r.Group):
		out = append(out, "group has leading or trailing spaces")
	case strings.Contains(r.Group, Wildcard):
		out = append(out, "group cannot be a pattern; name one group per rule")
	}

	out = append(out, names("environments", r.Environments)...)
	out = append(out, names("sites", r.Sites)...)

	if len(r.Allow) == 0 {
		out = append(out, "allow is empty")
	}

	for _, perm := range r.Allow {
		if !perm.Known() {
			out = append(out, fmt.Sprintf("unknown permission %q", perm))
		}
	}

	return out
}

// names checks one list of environment or site names.
func names(field string, list []string) []string {
	if len(list) == 0 {
		return []string{field + " is empty; use \"*\" for all"}
	}

	var out []string

	for _, name := range list {
		switch {
		case name == Wildcard:
		case strings.TrimSpace(name) == "":
			out = append(out, field+" has an empty name")
		case strings.Contains(name, Wildcard):
			out = append(out, fmt.Sprintf("%s: %q is not allowed; \"*\" only stands for a whole name", field, name))
		case name != strings.TrimSpace(name):
			out = append(out, fmt.Sprintf("%s: %q has leading or trailing spaces", field, name))
		}
	}

	return out
}

// covers reports whether the rule applies to the site in the environment for
// a person in groups.
func (r Rule) covers(groups []string, env, site string) bool {
	return r.Group != "" && slices.Contains(groups, r.Group) && matches(r.Environments, env) && matches(r.Sites, site)
}

// grants reports whether the rule gives perm. Every permission includes View,
// so a rule with any known permission grants View.
func (r Rule) grants(perm Permission) bool {
	if perm == View {
		return slices.ContainsFunc(r.Allow, Permission.Known)
	}

	return slices.Contains(r.Allow, perm)
}

// matches reports whether name is in list or list holds the wildcard. An
// empty name never matches, not even the wildcard.
func matches(list []string, name string) bool {
	if name == "" {
		return false
	}

	return slices.Contains(list, Wildcard) || slices.Contains(list, name)
}

// Allows reports whether a person in groups may do perm with the site in the
// environment. An unknown permission, an empty environment or site and a
// person with no groups are always refused.
func (p Policy) Allows(groups []string, env, site string, perm Permission) bool {
	if !perm.Known() {
		return false
	}

	return slices.ContainsFunc(p.Rules, func(r Rule) bool {
		return r.covers(groups, env, site) && r.grants(perm)
	})
}

// Permissions returns everything a person in groups may do with the site in
// the environment, in the order of All, or nil when
// they may do nothing. When it is not nil it starts with View.
func (p Policy) Permissions(groups []string, env, site string) []Permission {
	var out []Permission

	for _, perm := range All() {
		if p.Allows(groups, env, site, perm) {
			out = append(out, perm)
		}
	}

	return out
}

// Any reports whether a person in groups may see anything at all. The server
// shows a person for whom it is false a page that says so and nothing else.
func (p Policy) Any(groups []string) bool {
	return slices.ContainsFunc(p.Rules, func(r Rule) bool {
		return r.Group != "" && slices.Contains(groups, r.Group) && r.grants(View) &&
			len(r.Environments) > 0 && len(r.Sites) > 0
	})
}
