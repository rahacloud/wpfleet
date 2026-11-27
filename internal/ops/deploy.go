package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
)

// ErrSitesFailed is returned when some of the sites of a deploy or a diff
// failed; the others were still handled.
var ErrSitesFailed = errors.New("sites failed")

// Releases is what deploy and diff need from Helm. *helm.Client implements
// it; tests put a fake in its place.
type Releases interface {
	// Render renders a site's release without the cluster, hooks left out.
	Render(ctx context.Context, site, namespace string, valuesFiles []string) (string, error)
	// UpgradeInstall installs or upgrades a site's release.
	UpgradeInstall(
		ctx context.Context, site, namespace string, valuesFiles []string, opts helm.InstallOptions,
	) (helm.Release, error)
	// LiveManifest returns the manifest of a site's current release, hooks
	// left out.
	LiveManifest(site, namespace string) (string, error)
}

// releasesOf is the given Releases, or the environment's Helm client.
//
//nolint:ireturn // callers depend on the interface so tests can fake Helm
func releasesOf(env *Env, given Releases) (Releases, error) {
	if given != nil {
		return given, nil
	}

	h, err := env.Helm()
	if err != nil {
		return nil, err
	}

	return h, nil
}

// DeployOptions are the options of Deploy.
type DeployOptions struct {
	// Wait waits until each site's resources are ready.
	Wait bool
	// Releases deploys; nil uses the environment's Helm client.
	Releases Releases
	// Commit is the fleet commit to record in each release's description;
	// nil reads it from the fleet directory with FleetCommit.
	Commit *Commit
}

// DeployResult is the outcome of deploying one site.
type DeployResult struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Revision is the release's new revision; 0 when the deploy failed.
	Revision int `json:"revision"`
	// Status is Helm's status of the release, or failed.
	Status string `json:"status"`
	// Updated is when the revision was deployed.
	Updated time.Time `json:"updated"`
	// Error is why the deploy failed, when it did.
	Error string `json:"error,omitempty"`
}

// StatusFailed is the status of a site whose deploy returned an error.
const StatusFailed = "failed"

// DeployReport is what a deploy did.
type DeployReport struct {
	// Results has one entry per site, in the order deployed.
	Results []DeployResult
	// Commit is the fleet commit recorded in the releases.
	Commit Commit
	// Failed names the sites whose deploy failed.
	Failed []string
}

// Err is the error of a deploy in which some sites failed, wrapping
// ErrSitesFailed, or nil.
func (r DeployReport) Err() error {
	return failure(r.Failed, len(r.Results))
}

// Deploy installs or upgrades the release of each site, one after another,
// and records the fleet commit in each release's description. A failing site
// does not stop the others: it is in the report's Failed, and Err names them.
// The returned error is for what stopped the deploy as a whole: no Helm
// client, or ctx ending between two sites.
func Deploy(
	ctx context.Context, env *Env, sites []fleet.Site, opts DeployOptions, progress Progress,
) (DeployReport, error) {
	releases, err := releasesOf(env, opts.Releases)
	if err != nil {
		return DeployReport{Results: nil, Commit: Commit{SHA: "", Dirty: false}, Failed: nil}, err
	}

	commit := fleetCommit(env.Dir, opts.Commit)
	report := DeployReport{Results: make([]DeployResult, 0, len(sites)), Commit: commit, Failed: nil}
	install := helm.InstallOptions{Wait: opts.Wait, Description: commit.Description()}

	for _, site := range sites {
		if ctx.Err() != nil {
			return report, fmt.Errorf("deploy stopped before %s: %w", site.Name, context.Cause(ctx))
		}

		progress.emit(Event{
			Kind: EventDeploying, Env: env.Env.Name, Site: site.Name, Job: "", Namespace: "",
			Text: "deploying " + site.Name + " to " + env.Env.Name,
		})

		rel, err := releases.UpgradeInstall(ctx, site.Name, env.Env.Namespace, site.ValuesFiles, install)

		r := DeployResult{
			Site: site.Name, Env: env.Env.Name, Revision: rel.Revision, Status: rel.Status, Updated: rel.Updated, Error: "",
		}
		if err != nil {
			r.Status, r.Error = StatusFailed, err.Error()
			report.Failed = append(report.Failed, site.Name)
		}

		report.Results = append(report.Results, r)
	}

	return report, nil
}

// fleetCommit is the given commit, or the fleet directory's. A repository
// that cannot be read is logged and recorded as no commit: the deploy itself
// does not depend on it.
func fleetCommit(dir string, given *Commit) Commit {
	if given != nil {
		return *given
	}

	c, err := FleetCommit(dir)
	if err != nil {
		slog.Warn("cannot read the fleet commit; the release will not name one", "dir", dir, "err", err)
	}

	return c
}

// failure is the error of a run in which the named sites failed, or nil.
func failure(failed []string, total int) error {
	if len(failed) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %d of %d (%s)", ErrSitesFailed, len(failed), total, strings.Join(failed, ", "))
}
