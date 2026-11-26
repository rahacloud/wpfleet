// Package sites holds wpfleet sites: list, which shows every site of one or
// all environments, and show, which shows one site in detail.
package sites

import (
	"errors"
	"log/slog"
	"path/filepath"

	"github.com/urfave/cli/v3"
	"k8s.io/client-go/kubernetes"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Cluster is what the sites commands read from an environment's cluster: the
// pods of a site, and through the clientset its uploads claim and backup
// CronJob. *kube.Client implements it.
type Cluster interface {
	kube.Pods
	// Clientset is the cluster's clientset.
	Clientset() kubernetes.Interface
}

// Releases is what sites show needs from Helm. *helm.Client implements it.
type Releases interface {
	// Status returns what a site's release runs.
	Status(site, namespace string) (helm.Release, error)
}

// deps are how the commands reach an environment's cluster and releases;
// tests replace them with fakes.
type deps struct {
	cluster  func(env *cmdutil.Env) (Cluster, error)
	releases func(env *cmdutil.Env) (Releases, error)
}

// ErrNoClientset is returned when an environment's kube client cannot read
// objects other than pods.
var ErrNoClientset = errors.New("the environment's kube client has no clientset")

// kubeCluster is the Cluster of an environment: its kube client.
//
//nolint:ireturn // the commands depend on the interface so tests can fake the cluster
func kubeCluster(env *cmdutil.Env) (Cluster, error) {
	c, ok := env.Kube.(Cluster)
	if !ok {
		return nil, ErrNoClientset
	}

	return c, nil
}

// helmReleases is the Releases of an environment: its Helm client.
//
//nolint:ireturn // the commands depend on the interface so tests can fake Helm
func helmReleases(env *cmdutil.Env) (Releases, error) {
	h, err := env.Helm()
	if err != nil {
		return nil, err
	}

	return h, nil
}

// Command returns the wpfleet sites group.
func Command() *cli.Command {
	return command(deps{cluster: kubeCluster, releases: helmReleases})
}

// command builds the group on d.
func command(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:     "sites",
		Usage:    "list the sites of the fleet and show one in detail",
		Commands: []*cli.Command{listCommand(d), showCommand(d)},
	}
}

// chartDefaults loads the values.yaml of the fleet's chart, which every
// release starts from.
func chartDefaults(f *cmdutil.Fleet) (values.Tree, error) {
	path := f.Config.Chart
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(f.Dir, path)
	}

	ch, err := helm.LoadChart(path)
	if err != nil {
		return nil, err
	}

	return ch.Values, nil
}

// siteValues loads a site's values on top of the chart's defaults, as Helm
// renders them, and reads its host and image. A host the values cannot give
// is logged and left empty, so one broken site file does not hide the
// others.
func siteValues(defaults values.Tree, site fleet.Site) (values.Values, string, fleet.ImageRef, error) {
	files, err := values.Load(site.ValuesFiles...)
	if err != nil {
		return values.Values{}, "", fleet.ImageRef{}, err
	}

	v := values.New(values.Merge(defaults, files.Tree()))

	host, err := fleet.Host(v)
	if err != nil {
		slog.Warn("no host", "site", site.Name, "env", site.Env.Name, "err", err)
	}

	return v, host, fleet.Image(v), nil
}
