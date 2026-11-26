package sites

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/urfave/cli/v3"
	corev1 "k8s.io/api/core/v1"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// readyUnknown is the readiness of a site whose cluster could not be read.
const readyUnknown = "unknown"

// Row is one site in one environment, as sites list prints it.
type Row struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Host is the site's canonical host, from its values.
	Host string `json:"host"`
	// Image is the image its values deploy.
	Image string `json:"image"`
	// Ready is ready pods out of running ones, e.g. 1/1, or unknown when
	// the cluster could not be read.
	Ready string `json:"ready"`
}

// rowColumns are the columns of sites list.
func rowColumns() []cmdutil.Column[Row] {
	return []cmdutil.Column[Row]{
		{Header: "site", Value: func(r Row) string { return r.Site }},
		{Header: "env", Value: func(r Row) string { return r.Env }},
		{Header: "host", Value: func(r Row) string { return r.Host }},
		{Header: "image", Value: func(r Row) string { return r.Image }},
		{Header: "ready", Value: func(r Row) string { return r.Ready }},
	}
}

// listCommand builds wpfleet sites list.
func listCommand(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:  "list",
		Usage: "every site of --env, or of every environment without it: host, image and pod readiness",
		Description: "Host and image come from the values files; READY counts the site's ready pods out of " +
			"its running ones. A cluster that cannot be reached shows READY as unknown and is logged; " +
			"the rest of the list still prints.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runList(ctx, cmd, d)
		},
	}
}

// runList lists the sites of the selected environments, in the order of
// wpfleet.yaml, each environment's sites by name.
func runList(ctx context.Context, cmd *cli.Command, d deps) error {
	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	envs := f.Fleet.Environments

	if name := cmd.String(cmdutil.FlagEnv); name != "" {
		env, err := f.Fleet.Environment(name)
		if err != nil {
			return err
		}

		envs = []fleet.Environment{env}
	}

	defaults, err := chartDefaults(f)
	if err != nil {
		return err
	}

	var rows []Row

	for _, env := range envs {
		envRows, err := listEnv(ctx, f, defaults, env, d)
		if err != nil {
			return err
		}

		rows = append(rows, envRows...)
	}

	return cmdutil.Print(cmd, rows, rowColumns()...)
}

// listEnv lists the sites of one environment. After the first failure to
// read the cluster the remaining sites are not tried, so an unreachable
// cluster costs one timeout, not one per site.
func listEnv(
	ctx context.Context, f *cmdutil.Fleet, defaults values.Tree, env fleet.Environment, d deps,
) ([]Row, error) {
	sites, err := f.Sites(env)
	if err != nil {
		return nil, err
	}

	cluster := openCluster(f, env, d)
	rows := make([]Row, 0, len(sites))

	for _, site := range sites {
		_, host, image, err := siteValues(defaults, site)
		if err != nil {
			return nil, err
		}

		ready := readyUnknown

		if cluster != nil {
			pods, err := cluster.SitePods(ctx, site.Name)
			if err != nil {
				slog.Warn("cannot read pods; readiness unknown", "env", env.Name, "err", err)

				cluster = nil
			} else {
				ready = readiness(pods)
			}
		}

		rows = append(rows, Row{Site: site.Name, Env: env.Name, Host: host, Image: image.String(), Ready: ready})
	}

	return rows, nil
}

// openCluster opens the cluster of env, or logs why it cannot and returns
// nil: a missing kubeconfig for one environment must not hide the others.
//
//nolint:ireturn // nil stands for a cluster that cannot be read
func openCluster(f *cmdutil.Fleet, env fleet.Environment, d deps) Cluster {
	e, err := f.Open(env.Name)
	if err != nil {
		slog.Warn("cannot open the cluster; readiness unknown", "env", env.Name, "err", err)

		return nil
	}

	cluster, err := d.cluster(e)
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
