package values

import (
	"fmt"
	"os"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
)

// Sites lists the values directory of env and returns its sites, as
// fleet.DiscoverSites defines them.
func Sites(env fleet.Environment) ([]fleet.Site, error) {
	entries, err := os.ReadDir(env.ValuesDir)
	if err != nil {
		return nil, fmt.Errorf("environment %s: listing values: %w", env.Name, err)
	}

	names := make([]string, 0, len(entries))

	for _, e := range entries {
		if e.Type().IsRegular() || e.Type()&os.ModeSymlink != 0 {
			names = append(names, e.Name())
		}
	}

	sites, err := fleet.DiscoverSites(env, names)
	if err != nil {
		return nil, fmt.Errorf("environment %s: %w", env.Name, err)
	}

	return sites, nil
}
