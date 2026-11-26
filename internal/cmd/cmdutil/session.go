package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
)

// ErrNoSites is returned when a command that takes sites gets none.
var ErrNoSites = errors.New("name at least one site, or --all")

// ErrSitesAndAll is returned when a command gets both site names and --all.
var ErrSitesAndAll = errors.New("name sites or give --all, not both")

// Fleet is a loaded fleet repository.
type Fleet struct {
	// Dir is the fleet directory, as --fleet gave it.
	Dir string
	// Config is its wpfleet.yaml.
	Config config.Config
	// Fleet is the domain model of it.
	Fleet fleet.Fleet
}

// LoadFleet loads wpfleet.yaml from --fleet.
func LoadFleet(cmd *cli.Command) (*Fleet, error) {
	dir := cmd.String(FlagFleet)

	cfg, err := config.Load(dir, nil)
	if err != nil {
		return nil, err
	}

	slog.Debug("loaded fleet", "dir", dir, "environments", len(cfg.Environments))

	return &Fleet{Dir: dir, Config: cfg, Fleet: cfg.Fleet(dir)}, nil
}

// Sites discovers the sites of one environment.
func (f *Fleet) Sites(env fleet.Environment) ([]fleet.Site, error) {
	return values.Sites(env)
}

// WPCLI fetches wp-cli.phar as wpfleet.yaml says and returns a runner that
// copies it into pods through k.
func (f *Fleet) WPCLI(ctx context.Context, k kube.Execer) (*wpcli.Runner, error) {
	phar, err := wpcli.Ensure(ctx, wpcli.Source{
		URL:             f.Config.WPCLI.URL,
		SHA512:          f.Config.WPCLI.SHA512,
		AllowUnverified: f.Config.WPCLI.AllowUnverified,
	})
	if err != nil {
		return nil, err
	}

	return wpcli.NewRunner(k, phar), nil
}

// Env is the fleet with one environment selected, its sites discovered and a
// client for its cluster. Nothing contacts the cluster until a command calls
// Kube or Helm.
type Env struct {
	*Fleet

	// Env is the selected environment.
	Env fleet.Environment
	// Sites are the environment's sites, sorted by name.
	Sites []fleet.Site
	// Kube is the environment's cluster. It is an interface so that a test
	// can put kubetest in its place.
	Kube kube.Interface

	helm func() (*helm.Client, error)
}

// OpenEnv loads the fleet and selects the environment of --env.
func OpenEnv(cmd *cli.Command) (*Env, error) {
	name := cmd.String(FlagEnv)
	if name == "" {
		return nil, ErrNoEnv
	}

	f, err := LoadFleet(cmd)
	if err != nil {
		return nil, err
	}

	return f.Open(name)
}

// Open selects the named environment of the fleet.
func (f *Fleet) Open(name string) (*Env, error) {
	env, err := f.Fleet.Environment(name)
	if err != nil {
		return nil, err
	}

	sites, err := values.Sites(env)
	if err != nil {
		return nil, err
	}

	target := kube.TargetFor(env)

	client, err := kube.New(target)
	if err != nil {
		return nil, fmt.Errorf("environment %s: %w", name, err)
	}

	slog.Debug("opened environment", "env", name, "namespace", env.Namespace, "sites", len(sites))

	chart := f.chartPath()

	return &Env{
		Fleet: f,
		Env:   env,
		Sites: sites,
		Kube:  client,
		helm: sync.OnceValues(func() (*helm.Client, error) {
			ch, err := helm.LoadChart(chart)
			if err != nil {
				return nil, err
			}

			return helm.New(ch, target), nil
		}),
	}, nil
}

// Helm returns the Helm client of the environment, loading the chart on the
// first call.
func (e *Env) Helm() (*helm.Client, error) {
	return e.helm()
}

// Site returns one site of the environment.
func (e *Env) Site(name string) (fleet.Site, error) {
	sites, err := fleet.Select(e.Sites, []string{name})
	if err != nil {
		return fleet.Site{}, fmt.Errorf("environment %s: %w", e.Env.Name, err)
	}

	return sites[0], nil
}

// SelectSites returns the named sites, or every site with all. Exactly one of
// the two must be given.
func (e *Env) SelectSites(names []string, all bool) ([]fleet.Site, error) {
	switch {
	case all && len(names) > 0:
		return nil, ErrSitesAndAll
	case all:
		return e.Sites, nil
	case len(names) == 0:
		return nil, ErrNoSites
	}

	sites, err := fleet.Select(e.Sites, names)
	if err != nil {
		return nil, fmt.Errorf("environment %s: %w", e.Env.Name, err)
	}

	return sites, nil
}

// Values loads a site's merged values.
func (e *Env) Values(site fleet.Site) (values.Values, error) {
	return values.Load(site.ValuesFiles...)
}

// ChartValues returns a site's values over the chart's defaults, which is
// what the chart's templates see: the host, the scheme, the backup images
// and the S3 secret name may all come from the defaults.
func (e *Env) ChartValues(site fleet.Site) (values.Values, error) {
	v, err := e.Values(site)
	if err != nil {
		return values.Values{}, err
	}

	h, err := e.Helm()
	if err != nil {
		return values.Values{}, err
	}

	return values.New(values.Merge(h.Chart().Values, v.Tree())), nil
}

// valueTLS says whether the chart serves a site over HTTPS.
const valueTLS = "ingress.tls.enabled"

// SiteURL is the canonical URL of a site from its values over the chart's
// defaults: the host as the chart's wordpress.hosts helper gives it, over
// HTTPS unless ingress.tls.enabled is false.
func SiteURL(v values.Values) (string, error) {
	host, err := fleet.Host(v)
	if err != nil {
		return "", err
	}

	scheme := "https"

	// The chart's default is on; only an explicit false turns it off.
	if tls, ok := values.Get[bool](v, valueTLS); ok && !tls {
		scheme = "http"
	}

	return scheme + "://" + host, nil
}

// chartPath resolves the configured chart against the fleet directory.
func (f *Fleet) chartPath() string {
	if f.Config.Chart == "" || filepath.IsAbs(f.Config.Chart) {
		return f.Config.Chart
	}

	return filepath.Join(f.Dir, f.Config.Chart)
}
