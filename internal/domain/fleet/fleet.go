// Package fleet is the model of a fleet: its environments, the sites in each
// environment, how a site's host and image are derived from its values, and
// what a promotion between environments changes. It does no I/O; the infra
// packages read the files and the cluster and hand the results in.
package fleet

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// SharedValuesFile is the file in an environment's values directory that every
// site of the environment shares. It is rendered before the site's own file.
const SharedValuesFile = "_shared.yaml"

// valuesExt is the extension of every values file.
const valuesExt = ".yaml"

// maxLabel is the longest DNS label.
const maxLabel = 63

// dnsLabel is an RFC 1123 label in lowercase, the form the chart accepts for a
// site name.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ErrUnknownEnvironment is returned when an environment name is not in the
// fleet.
var ErrUnknownEnvironment = errors.New("unknown environment")

// ErrUnknownSite is returned when a site name is not in an environment.
var ErrUnknownSite = errors.New("unknown site")

// ErrInvalidSiteName is returned for a site name that is not a DNS label.
var ErrInvalidSiteName = errors.New("site name must be a DNS label: lowercase letters, digits and hyphens, at most 63")

// ErrNoPromotionSource is returned when an environment has no promoteFrom.
var ErrNoPromotionSource = errors.New("environment has no promoteFrom")

// Environment is one environment of the fleet: a namespace in a cluster and
// the directory that holds its values files.
type Environment struct {
	// Name is the environment's name, e.g. staging.
	Name string `json:"name"`
	// Namespace is the Kubernetes namespace every release of the environment
	// lives in.
	Namespace string `json:"namespace"`
	// Kubeconfig is the kubeconfig file; empty follows the usual KUBECONFIG
	// rules.
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// Context is the kubeconfig context; empty uses the current one.
	Context string `json:"context,omitempty"`
	// ValuesDir is the directory of the environment's values files.
	ValuesDir string `json:"valuesDir"`
	// PromoteFrom names the environment whose images promote into this one.
	PromoteFrom string `json:"promoteFrom,omitempty"`
}

// Fleet is every environment of a fleet repository.
type Fleet struct {
	// Root is the fleet repository's directory.
	Root string `json:"root"`
	// Registry is where site images live, as <Registry>/<site>:<tag>.
	Registry string `json:"registry"`
	// Environments are in the order wpfleet.yaml lists them.
	Environments []Environment `json:"environments"`
}

// Environment returns the environment with the given name.
func (f Fleet) Environment(name string) (Environment, error) {
	i := slices.IndexFunc(f.Environments, func(e Environment) bool { return e.Name == name })
	if i < 0 {
		known := strings.Join(f.EnvironmentNames(), ", ")

		return Environment{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownEnvironment, name, known)
	}

	return f.Environments[i], nil
}

// EnvironmentNames lists the environment names in order.
func (f Fleet) EnvironmentNames() []string {
	names := make([]string, 0, len(f.Environments))
	for _, e := range f.Environments {
		names = append(names, e.Name)
	}

	return names
}

// PromotionSource returns the environment that the named environment promotes
// from.
func (f Fleet) PromotionSource(name string) (Environment, error) {
	env, err := f.Environment(name)
	if err != nil {
		return Environment{}, err
	}

	if env.PromoteFrom == "" {
		return Environment{}, fmt.Errorf("%s: %w", name, ErrNoPromotionSource)
	}

	return f.Environment(env.PromoteFrom)
}

// Image is the image reference of a site built into the fleet's registry.
func (f Fleet) Image(site, tag string) ImageRef {
	return ImageRef{Repository: strings.TrimSuffix(f.Registry, "/") + "/" + site, Tag: tag, Digest: ""}
}

// Site is one site in one environment, rendered as one Helm release.
type Site struct {
	// Name is the site's name, the stem of its values file and the release
	// name.
	Name string `json:"name"`
	// Env is the environment the site belongs to.
	Env Environment `json:"env"`
	// ValuesFiles are the files to render the release from, the shared file
	// first and the site's own file last.
	ValuesFiles []string `json:"valuesFiles"`
}

// File is the site's own values file, the last of ValuesFiles.
func (s Site) File() string {
	if len(s.ValuesFiles) == 0 {
		return ""
	}

	return s.ValuesFiles[len(s.ValuesFiles)-1]
}

// ValidateSiteName reports whether name can be a site: a lowercase DNS label.
func ValidateSiteName(name string) error {
	if len(name) > maxLabel || !dnsLabel.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrInvalidSiteName)
	}

	return nil
}

// DiscoverSites builds the sites of env from the names of the files in its
// values directory. Every *.yaml file except _shared.yaml is a site named by
// its stem; other files are ignored. Each site renders from _shared.yaml, when
// the listing has one, and then its own file. The sites come back sorted by
// name.
func DiscoverSites(env Environment, fileNames []string) ([]Site, error) {
	shared := slices.Contains(fileNames, SharedValuesFile)

	var (
		sites []Site
		errs  []error
	)

	for _, file := range fileNames {
		stem, ok := strings.CutSuffix(file, valuesExt)
		if !ok || file == SharedValuesFile || strings.Contains(file, "/") {
			continue
		}

		err := ValidateSiteName(stem)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Join(env.ValuesDir, file), err))

			continue
		}

		files := make([]string, 0, 2) //nolint:mnd // the shared file and the site's own
		if shared {
			files = append(files, filepath.Join(env.ValuesDir, SharedValuesFile))
		}

		sites = append(sites, Site{Name: stem, Env: env, ValuesFiles: append(files, filepath.Join(env.ValuesDir, file))})
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	slices.SortFunc(sites, func(a, b Site) int { return strings.Compare(a.Name, b.Name) })

	return sites, nil
}

// Select returns the named sites from sites, in the order of names. An unknown
// name is an error that lists every unknown one.
func Select(sites []Site, names []string) ([]Site, error) {
	out := make([]Site, 0, len(names))

	var missing []string

	for _, name := range names {
		i := slices.IndexFunc(sites, func(s Site) bool { return s.Name == name })
		if i < 0 {
			missing = append(missing, name)

			continue
		}

		out = append(out, sites[i])
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSite, strings.Join(missing, ", "))
	}

	return out, nil
}
