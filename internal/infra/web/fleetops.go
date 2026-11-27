package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// FleetOps runs the operations of internal/ops on the fleet in a directory,
// the server's clone. It loads the fleet again on every call, so a sync of
// the clone shows at the next request.
type FleetOps struct {
	// Dir returns the fleet directory.
	Dir func() string
	// Getenv reads the S3 keys; nil uses os.Getenv.
	Getenv func(string) string
}

// Interface check.
var _ Operations = FleetOps{Dir: nil, Getenv: nil}

// Environments lists the fleet's environments.
func (o FleetOps) Environments() ([]Environment, error) {
	f, err := o.load()
	if err != nil {
		return nil, err
	}

	envs := make([]Environment, 0, len(f.Fleet.Environments))
	for _, e := range f.Fleet.Environments {
		envs = append(envs, Environment{Name: e.Name, PromoteFrom: e.PromoteFrom})
	}

	return envs, nil
}

// Sites lists the names of env's sites.
func (o FleetOps) Sites(env string) ([]string, error) {
	f, err := o.load()
	if err != nil {
		return nil, err
	}

	e, err := f.Fleet.Environment(env)
	if err != nil {
		return nil, err
	}

	sites, err := f.Sites(e)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(sites))
	for _, s := range sites {
		names = append(names, s.Name)
	}

	return names, nil
}

// ListSites lists the sites of envs.
func (o FleetOps) ListSites(ctx context.Context, envs []string) ([]ops.SiteRow, error) {
	f, err := o.load()
	if err != nil {
		return nil, err
	}

	list := make([]fleet.Environment, 0, len(envs))

	for _, name := range envs {
		e, err := f.Fleet.Environment(name)
		if err != nil {
			return nil, err
		}

		list = append(list, e)
	}

	return ops.ListSites(ctx, f, list, ops.ListOptions{Cluster: nil})
}

// ShowSite reads one site in detail.
func (o FleetOps) ShowSite(ctx context.Context, env, site string) (ops.SiteDetail, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return ops.SiteDetail{}, err
	}

	return ops.ShowSite(ctx, e, s, ops.ShowOptions{Cluster: nil, Releases: nil})
}

// Diff compares a site's release with what the fleet renders.
func (o FleetOps) Diff(ctx context.Context, env, site string) (ops.SiteDiff, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return ops.SiteDiff{}, err
	}

	report, err := ops.Diff(ctx, e, []fleet.Site{s}, ops.DiffOptions{Releases: nil})
	if err != nil {
		return ops.SiteDiff{}, err
	}

	return report.Diffs[0], nil
}

// ListBackups lists a site's backups.
func (o FleetOps) ListBackups(ctx context.Context, env, site string) ([]ops.BackupRow, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return nil, err
	}

	return ops.ListBackups(ctx, e, s, o.backupOptions())
}

// SiteFile is the site's own values file, relative to the fleet directory.
func (o FleetOps) SiteFile(env, site string) (string, error) {
	f, err := o.load()
	if err != nil {
		return "", err
	}

	e, err := f.Fleet.Environment(env)
	if err != nil {
		return "", err
	}

	sites, err := f.Sites(e)
	if err != nil {
		return "", err
	}

	i := slices.IndexFunc(sites, func(s fleet.Site) bool { return s.Name == site })
	if i < 0 {
		return "", fmt.Errorf("%s in %s: %w", site, env, fleet.ErrUnknownSite)
	}

	files := sites[i].ValuesFiles

	return relativeTo(o.Dir(), files[len(files)-1])
}

// relativeTo gives file relative to dir, for Git.
func relativeTo(dir, file string) (string, error) {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return "", fmt.Errorf("%s relative to %s: %w", file, dir, err)
	}

	return filepath.ToSlash(rel), nil
}

// PlanPromotion says what publishing site into to changes.
func (o FleetOps) PlanPromotion(to, site string) (fleet.PromotionPlan, error) {
	f, err := o.load()
	if err != nil {
		return fleet.PromotionPlan{}, err
	}

	p, err := ops.PlanPromotion(f, to, []string{site})
	if err != nil {
		return fleet.PromotionPlan{}, err
	}

	return p.Plans[0], nil
}

// Promote publishes site into to in the fleet at dir, which is the clone
// that Repo.Commit hands its edit, and returns the changed files relative
// to dir.
func (o FleetOps) Promote(ctx context.Context, dir, to, site string) ([]string, error) {
	f, err := ops.LoadFleet(dir)
	if err != nil {
		return nil, err
	}

	p, err := ops.PlanPromotion(f, to, []string{site})
	if err != nil {
		return nil, err
	}

	_, err = ops.ApplyPromotion(ctx, p)
	if err != nil {
		return nil, err
	}

	files := p.Files()
	rel := make([]string, 0, len(files))

	for _, file := range files {
		r, err := relativeTo(dir, file)
		if err != nil {
			return nil, err
		}

		rel = append(rel, r)
	}

	return rel, nil
}

// Deploy deploys a site and waits for it to be ready, recording commit.
func (o FleetOps) Deploy(
	ctx context.Context, env, site string, commit ops.Commit, progress ops.Progress,
) (ops.DeployResult, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return ops.DeployResult{}, err
	}

	opts := ops.DeployOptions{Wait: true, Releases: nil, Commit: &commit}

	report, err := ops.Deploy(ctx, e, []fleet.Site{s}, opts, progress)
	if err != nil {
		return ops.DeployResult{}, err
	}

	return report.Results[0], nil
}

// AdminLogin makes a one-time login link.
func (o FleetOps) AdminLogin(ctx context.Context, env, site, matchEmail string) (ops.Login, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return ops.Login{}, err
	}

	return ops.AdminLogin(ctx, e, s, ops.AdminOptions{User: "", MatchEmail: matchEmail, Pods: nil, WP: nil, Random: nil})
}

// RunBackup takes a backup now.
func (o FleetOps) RunBackup(ctx context.Context, env, site string, progress ops.Progress) (ops.BackupRun, error) {
	e, s, err := o.site(env, site)
	if err != nil {
		return ops.BackupRun{}, err
	}

	return ops.RunBackup(ctx, e, s, o.backupOptions(), progress)
}

// Restore plans the restore of stamp and runs it.
func (o FleetOps) Restore(ctx context.Context, env, site, stamp string, uploads bool, progress ops.Progress) error {
	e, s, err := o.site(env, site)
	if err != nil {
		return err
	}

	opts := o.backupOptions()

	spec, err := ops.PlanRestore(ctx, e, s, ops.RestoreOptions{BackupOptions: opts, Stamp: stamp, Uploads: uploads})
	if err != nil {
		return err
	}

	return ops.Restore(ctx, e, spec, opts, progress)
}

// load reads the fleet at the directory.
func (o FleetOps) load() (*ops.Fleet, error) {
	return ops.LoadFleet(o.Dir())
}

// site opens env and finds site in it.
func (o FleetOps) site(env, site string) (*ops.Env, fleet.Site, error) {
	f, err := o.load()
	if err != nil {
		return nil, fleet.Site{}, err
	}

	e, err := f.Open(env)
	if err != nil {
		return nil, fleet.Site{}, err
	}

	s, err := e.Site(site)
	if err != nil {
		return nil, fleet.Site{}, err
	}

	return e, s, nil
}

// backupOptions are the defaults of internal/ops with the S3 keys from
// Getenv.
func (o FleetOps) backupOptions() ops.BackupOptions {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	return ops.BackupOptions{Now: nil, Poll: 0, Getenv: getenv}
}
