package ops

import (
	"errors"
	"log/slog"

	"k8s.io/client-go/kubernetes"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Cluster is what the sites list and detail read from an environment's
// cluster: the pods of a site, and through the clientset its uploads claim
// and backup CronJob. *kube.Client implements it.
type Cluster interface {
	kube.Pods
	// Clientset is the cluster's clientset.
	Clientset() kubernetes.Interface
}

// ReleaseStatus is what the site detail needs from Helm. *helm.Client
// implements it.
type ReleaseStatus interface {
	// Status returns what a site's release runs.
	Status(site, namespace string) (helm.Release, error)
}

// ErrNoClientset is returned when an environment's kube client cannot read
// objects other than pods.
var ErrNoClientset = errors.New("the environment's kube client has no clientset")

// kubeCluster is the Cluster of an environment: its kube client.
//
//nolint:ireturn // callers depend on the interface so tests can fake the cluster
func kubeCluster(env *Env) (Cluster, error) {
	c, ok := env.Kube.(Cluster)
	if !ok {
		return nil, ErrNoClientset
	}

	return c, nil
}

// statusOf is the given ReleaseStatus, or the environment's Helm client.
//
//nolint:ireturn // callers depend on the interface so tests can fake Helm
func statusOf(env *Env, given ReleaseStatus) (ReleaseStatus, error) {
	if given != nil {
		return given, nil
	}

	h, err := env.Helm()
	if err != nil {
		return nil, err
	}

	return h, nil
}

// chartDefaults loads the values.yaml of the fleet's chart, which every
// release starts from.
func chartDefaults(f *Fleet) (values.Tree, error) {
	ch, err := helm.LoadChart(f.ChartPath())
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
