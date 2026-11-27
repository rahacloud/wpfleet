package ops

import (
	"context"
	"errors"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
)

// DiffOptions are the options of Diff.
type DiffOptions struct {
	// Releases renders and reads the live releases; nil uses the
	// environment's Helm client.
	Releases Releases
}

// SiteDiff is what a deploy would change on one site.
type SiteDiff struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Installed says whether the site has a release.
	Installed bool `json:"installed"`
	// Changed says whether a deploy would change anything.
	Changed bool `json:"changed"`
	// Diff is the unified diff from the live manifest to the rendered one.
	Diff string `json:"diff,omitempty"`
	// Error is why the site could not be compared, when it could not.
	Error string `json:"error,omitempty"`
}

// DiffReport is what a diff found.
type DiffReport struct {
	// Diffs has one entry per site, in the order given.
	Diffs []SiteDiff
	// Failed names the sites that could not be compared.
	Failed []string
}

// Err is the error of a diff in which some sites could not be compared,
// wrapping ErrSitesFailed, or nil.
func (r DiffReport) Err() error {
	return failure(r.Failed, len(r.Diffs))
}

// Changed says whether a deploy would change any site.
func (r DiffReport) Changed() bool {
	for _, d := range r.Diffs {
		if d.Changed {
			return true
		}
	}

	return false
}

// Diff compares each site's live release with what its values render now:
// hooks left out, blank lines and document order ignored, the values of
// Secrets redacted with a key made for this call. A site without a release
// diffs against nothing. A site that cannot be compared is in Failed; the
// returned error is only for a missing Helm client.
func Diff(ctx context.Context, env *Env, sites []fleet.Site, opts DiffOptions) (DiffReport, error) {
	releases, err := releasesOf(env, opts.Releases)
	if err != nil {
		return DiffReport{Diffs: nil, Failed: nil}, err
	}

	r := newRedactor()
	report := DiffReport{Diffs: make([]SiteDiff, 0, len(sites)), Failed: nil}

	for _, site := range sites {
		d := diffSite(ctx, releases, r, env.Env.Name, site)
		if d.Error != "" {
			report.Failed = append(report.Failed, site.Name)
		}

		report.Diffs = append(report.Diffs, d)
	}

	return report, nil
}

// diffSite compares one site's live release with its rendering.
func diffSite(ctx context.Context, releases Releases, r redactor, envName string, site fleet.Site) SiteDiff {
	d := SiteDiff{Site: site.Name, Env: envName, Installed: true, Changed: false, Diff: "", Error: ""}

	rendered, err := releases.Render(ctx, site.Name, site.Env.Namespace, site.ValuesFiles)
	if err != nil {
		d.Error = err.Error()

		return d
	}

	live, err := releases.LiveManifest(site.Name, site.Env.Namespace)

	switch {
	case errors.Is(err, helm.ErrReleaseNotFound):
		d.Installed, live = false, ""
	case err != nil:
		d.Error = err.Error()

		return d
	}

	prefix := envName + "/" + site.Name

	d.Diff, err = unifiedDiff(r.normalize(live), r.normalize(rendered), prefix+" (live)", prefix+" (rendered)")
	if err != nil {
		d.Error = err.Error()

		return d
	}

	d.Changed = d.Diff != ""

	return d
}
