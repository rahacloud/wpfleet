package serve

import (
	"context"
	"slices"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/infra/oidc"
)

// groupFilter signs people in through the provider and keeps only the
// groups some access rule names. The others can never grant anything, and a
// person in hundreds of groups would push the session cookie past the 4 KiB
// a browser keeps.
type groupFilter struct {
	*oidc.Provider

	named map[string]bool
}

// ruleGroups is the provider with the groups limited to those policy names.
func ruleGroups(p *oidc.Provider, policy access.Policy) groupFilter {
	named := make(map[string]bool, len(policy.Rules))
	for _, r := range policy.Rules {
		named[r.Group] = true
	}

	return groupFilter{Provider: p, named: named}
}

// Exchange redeems the code and drops the groups no rule names.
func (g groupFilter) Exchange(ctx context.Context, code, verifier, nonce string) (access.Identity, error) {
	id, err := g.Provider.Exchange(ctx, code, verifier, nonce)
	if err != nil {
		return access.Identity{}, err
	}

	id.Groups = slices.DeleteFunc(id.Groups, func(group string) bool { return !g.named[group] })

	return id, nil
}
