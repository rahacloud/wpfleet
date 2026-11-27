package ops

import (
	"context"
	"log/slog"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// ReadyUnknown is the readiness of a site whose cluster could not be read.
const ReadyUnknown = "unknown"

// SiteRow is one site in one environment, as the sites list shows it.
type SiteRow struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Host is the site's canonical host, from its values.
	Host string `json:"host"`
	// URL is the site's address with the scheme its ingress serves (see
	// SiteURL); empty when the values give no host.
	URL string `json:"url"`
	// Image is the image its values deploy.
	Image string `json:"image"`
	// Ready is ready pods out of running ones, e.g. 1/1, or unknown when
	// the cluster could not be read.
	Ready string `json:"ready"`
}

// ListOptions are the options of ListSites.
type ListOptions struct {
	// Cluster gives an environment's cluster; nil uses its kube client.
	Cluster func(env *Env) (Cluster, error)
}

// ListSites lists the sites of envs, in the order given, each environment's
// sites by name: host and image from the values over the chart's defaults,
// and pod readiness. An environment whose cluster cannot be opened or read
// is logged as a warning and its sites show readiness as ReadyUnknown; the
// rest still lists. After the first failure to read a cluster the remaining
// sites of that environment are not tried, so an unreachable cluster costs
// one timeout, not one per site.
func ListSites(ctx context.Context, f *Fleet, envs []fleet.Environment, opts ListOptions) ([]SiteRow, error) {
	clusterOf := opts.Cluster
	if clusterOf == nil {
		clusterOf = kubeCluster
	}

	defaults, err := chartDefaults(f)
	if err != nil {
		return nil, err
	}

	var rows []SiteRow

	for _, env := range envs {
		envRows, err := listEnv(ctx, f, defaults, env, clusterOf)
		if err != nil {
			return nil, err
		}

		rows = append(rows, envRows...)
	}

	return rows, nil
}

// listEnv lists the sites of one environment. After the first failure to
// read the cluster the remaining sites are not tried, so an unreachable
// cluster costs one timeout, not one per site.
func listEnv(
	ctx context.Context, f *Fleet, defaults values.Tree, env fleet.Environment, clusterOf func(*Env) (Cluster, error),
) ([]SiteRow, error) {
	sites, err := f.Sites(env)
	if err != nil {
		return nil, err
	}

	cluster := openCluster(f, env, clusterOf)
	rows := make([]SiteRow, 0, len(sites))

	for _, site := range sites {
		v, host, image, err := siteValues(defaults, site)
		if err != nil {
			return nil, err
		}

		ready := ReadyUnknown

		if cluster != nil {
			pods, err := cluster.SitePods(ctx, site.Name)
			if err != nil {
				slog.Warn("cannot read pods; readiness unknown", "env", env.Name, "err", err)

				cluster = nil
			} else {
				ready = readiness(pods)
			}
		}

		rows = append(rows, SiteRow{
			Site: site.Name, Env: env.Name, Host: host, URL: urlOf(v, host), Image: image.String(), Ready: ready,
		})
	}

	return rows, nil
}

// openCluster opens the cluster of env, or logs why it cannot and returns
// nil: a missing kubeconfig for one environment must not hide the others.
//
//nolint:ireturn // nil stands for a cluster that cannot be read
func openCluster(f *Fleet, env fleet.Environment, clusterOf func(*Env) (Cluster, error)) Cluster {
	e, err := f.Open(env.Name)
	if err != nil {
		slog.Warn("cannot open the cluster; readiness unknown", "env", env.Name, "err", err)

		return nil
	}

	cluster, err := clusterOf(e)
	if err != nil {
		slog.Warn("cannot open the cluster; readiness unknown", "env", env.Name, "err", err)

		return nil
	}

	return cluster
}

// readiness counts the ready pods out of those that run or are starting:
// finished pods and pods being deleted are left out.
func readiness(pods []corev1.Pod) string {
	var running, ready int

	for _, p := range pods {
		if p.DeletionTimestamp != nil || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}

		running++

		if kube.Ready(p) {
			ready++
		}
	}

	return strconv.Itoa(ready) + "/" + strconv.Itoa(running)
}
