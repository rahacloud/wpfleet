// Package helm renders and installs site releases with the Helm Go SDK, and
// reads what a release runs in the cluster.
package helm

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartloader "helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/ignore"
	helmkube "helm.sh/helm/v4/pkg/kube"
	releasei "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"

	"github.com/rahacloud/wpfleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// storageDriver keeps release records in Secrets, as the helm CLI does.
const storageDriver = "secret"

// defaultTimeout bounds hooks and, with wait, readiness.
const defaultTimeout = 10 * time.Minute

// maxHistory is how many revisions of a release are kept.
const maxHistory = 10

// ErrReleaseType is returned when the SDK hands back a release type this
// package does not know.
var ErrReleaseType = errors.New("unsupported release type")

// ErrReleaseNotFound is returned for a site that has no release.
var ErrReleaseNotFound = driver.ErrReleaseNotFound

// LoadChart loads the chart at path, a directory or an archive; an empty path
// loads the chart embedded in the binary.
func LoadChart(path string) (*chartv2.Chart, error) {
	if path != "" {
		ch, err := chartloader.Load(path)
		if err != nil {
			return nil, fmt.Errorf("loading chart %s: %w", path, err)
		}

		return ch, nil
	}

	root, err := fs.Sub(wpfleet.Chart, wpfleet.ChartRoot)
	if err != nil {
		return nil, fmt.Errorf("opening embedded chart: %w", err)
	}

	return LoadChartFS(root)
}

// LoadChartFS loads a chart from the root of fsys, leaving out what its
// .helmignore names, as helm package does.
func LoadChartFS(fsys fs.FS) (*chartv2.Chart, error) {
	rules, err := ignoreRules(fsys)
	if err != nil {
		return nil, err
	}

	var files []*archive.BufferedFile

	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || name == "." {
			return err
		}

		file, skip, err := chartFile(fsys, rules, name, d)
		if file != nil {
			files = append(files, file)
		}

		if skip {
			return fs.SkipDir
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("reading chart files: %w", err)
	}

	ch, err := chartloader.LoadFiles(files)
	if err != nil {
		return nil, fmt.Errorf("loading chart: %w", err)
	}

	return ch, nil
}

// ignoreRules reads the chart's .helmignore, with Helm's defaults added.
func ignoreRules(fsys fs.FS) (*ignore.Rules, error) {
	rules := ignore.Empty()

	f, err := fsys.Open(ignore.HelmIgnore)
	if err == nil {
		rules, err = ignore.Parse(f)
		_ = f.Close()

		if err != nil {
			return nil, fmt.Errorf("reading .helmignore: %w", err)
		}
	}

	rules.AddDefaults()

	return rules, nil
}

// chartFile reads one walked entry: the file to load, or whether to skip an
// ignored directory.
func chartFile(fsys fs.FS, rules *ignore.Rules, name string, d fs.DirEntry) (*archive.BufferedFile, bool, error) {
	info, err := d.Info()
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", name, err)
	}

	if rules.Ignore(name, info) {
		return nil, d.IsDir(), nil
	}

	if d.IsDir() {
		return nil, false, nil
	}

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", name, err)
	}

	return &archive.BufferedFile{Name: name, ModTime: info.ModTime(), Data: data}, false, nil
}

// Release is what a release runs.
type Release struct {
	// Name is the release name, the site's.
	Name string `json:"name"`
	// Namespace is the release's namespace.
	Namespace string `json:"namespace"`
	// Revision is the release's revision number.
	Revision int `json:"revision"`
	// Status is Helm's status, e.g. deployed or failed.
	Status string `json:"status"`
	// ChartVersion is the version of the chart it was rendered from.
	ChartVersion string `json:"chartVersion"`
	// Updated is when the revision was deployed.
	Updated time.Time `json:"updated"`
	// Description is Helm's description of the revision; wpfleet's deploys
	// write "wpfleet <commit>" there. The CLI's JSON leaves it out, so its
	// output keeps the shape it had before deploys recorded commits.
	Description string `json:"-"`
}

// InstallOptions are the options of UpgradeInstall.
type InstallOptions struct {
	// Wait waits until the resources are ready; without it, only for the
	// hooks.
	Wait bool
	// Description is stored as the new revision's description; empty keeps
	// Helm's own ("Install complete", "Upgrade complete").
	Description string
}

// configurer makes the action configuration for a namespace.
type configurer func(namespace string) (*action.Configuration, error)

// Client renders and installs releases of one chart in one cluster.
type Client struct {
	chart     *chartv2.Chart
	configure configurer
	// Timeout bounds hooks and, with wait, readiness.
	Timeout time.Duration
}

// New makes a client for the chart and the cluster of target. It does not
// contact the cluster until an action needs it.
func New(ch *chartv2.Chart, target kube.Target) *Client {
	return &Client{
		chart: ch,
		configure: func(namespace string) (*action.Configuration, error) {
			cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.Default().Handler()))
			err := cfg.Init(target.RESTClientGetter(), namespace, storageDriver)
			if err != nil {
				return nil, fmt.Errorf("connecting Helm: %w", err)
			}

			return cfg, nil
		},
		Timeout: defaultTimeout,
	}
}

// Chart is the chart the client renders.
func (c *Client) Chart() *chartv2.Chart {
	return c.chart
}

// Render renders the release of site from the values files without the
// cluster, like helm template, and returns the manifest without hooks.
func (c *Client) Render(ctx context.Context, site, namespace string, valuesFiles []string) (string, error) {
	vals, err := values.Load(valuesFiles...)
	if err != nil {
		return "", err
	}

	cfg := action.NewConfiguration(action.ConfigurationSetLogger(slog.Default().Handler()))

	install := action.NewInstall(cfg)
	install.DryRunStrategy = action.DryRunClient
	install.Replace = true
	install.ReleaseName = site
	install.Namespace = namespace

	rel, err := install.RunWithContext(ctx, c.chart, vals.Tree())
	if err != nil {
		return "", fmt.Errorf("rendering %s: %w", site, err)
	}

	r, err := toV1(rel)
	if err != nil {
		return "", err
	}

	return r.Manifest, nil
}

// UpgradeInstall installs the release of site, or upgrades it when it exists,
// from the values files, as opts says.
func (c *Client) UpgradeInstall(
	ctx context.Context, site, namespace string, valuesFiles []string, opts InstallOptions,
) (Release, error) {
	vals, err := values.Load(valuesFiles...)
	if err != nil {
		return Release{}, err
	}

	cfg, err := c.configure(namespace)
	if err != nil {
		return Release{}, err
	}

	strategy := helmkube.HookOnlyStrategy
	if opts.Wait {
		strategy = helmkube.StatusWatcherStrategy
	}

	versions, err := action.NewHistory(cfg).Run(site)
	replace := uninstalled(newest(versions))

	var rel releasei.Releaser

	switch {
	case errors.Is(err, driver.ErrReleaseNotFound) || replace:
		install := action.NewInstall(cfg)
		install.ReleaseName = site
		install.Namespace = namespace
		install.Replace = replace
		install.WaitStrategy = strategy
		install.Timeout = c.Timeout
		install.ServerSideApply = true
		install.Description = opts.Description

		rel, err = install.RunWithContext(ctx, c.chart, vals.Tree())
	case err != nil:
		return Release{}, fmt.Errorf("reading history of %s: %w", site, err)
	default:
		upgrade := action.NewUpgrade(cfg)
		upgrade.Namespace = namespace
		upgrade.WaitStrategy = strategy
		upgrade.Timeout = c.Timeout
		upgrade.ServerSideApply = "auto"
		upgrade.ResetValues = true
		upgrade.MaxHistory = maxHistory
		upgrade.Description = opts.Description

		rel, err = upgrade.RunWithContext(ctx, site, c.chart, vals.Tree())
	}

	if err != nil {
		return Release{}, fmt.Errorf("deploying %s: %w", site, err)
	}

	return summarize(rel)
}

// LiveManifest returns the manifest of the release's last deployed revision,
// the one the cluster runs, hooks left out. A newer revision that failed or
// is pending was never fully applied, so it does not count; a release with
// no deployed revision is ErrReleaseNotFound.
func (c *Client) LiveManifest(site, namespace string) (string, error) {
	cfg, err := c.configure(namespace)
	if err != nil {
		return "", err
	}

	rel, err := cfg.Releases.Deployed(site)

	switch {
	case errors.Is(err, driver.ErrNoDeployedReleases):
		return "", fmt.Errorf("release %s has no deployed revision: %w", site, ErrReleaseNotFound)
	case err != nil:
		return "", fmt.Errorf("reading release %s: %w", site, err)
	}

	r, err := toV1(rel)
	if err != nil {
		return "", err
	}

	return r.Manifest, nil
}

// Status returns what the release of site runs.
func (c *Client) Status(site, namespace string) (Release, error) {
	cfg, err := c.configure(namespace)
	if err != nil {
		return Release{}, err
	}

	rel, err := action.NewStatus(cfg).Run(site)
	if err != nil {
		return Release{}, fmt.Errorf("reading release %s: %w", site, err)
	}

	return summarize(rel)
}

// History returns the newest max revisions of the release of site, newest
// first; max 0 or less returns every revision Helm keeps. A site without a
// release is ErrReleaseNotFound.
func (c *Client) History(site, namespace string, maxRevisions int) ([]Release, error) {
	cfg, err := c.configure(namespace)
	if err != nil {
		return nil, err
	}

	versions, err := action.NewHistory(cfg).Run(site)
	if err != nil {
		return nil, fmt.Errorf("reading the history of release %s: %w", site, err)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("reading the history of release %s: %w", site, ErrReleaseNotFound)
	}

	out := make([]Release, 0, len(versions))

	for _, v := range versions {
		r, err := summarize(v)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	// The storage drivers list revisions in their own order (the Secret
	// driver by name, so v10 before v9), not by number.
	slices.SortFunc(out, func(a, b Release) int { return cmp.Compare(b.Revision, a.Revision) })

	if maxRevisions > 0 && len(out) > maxRevisions {
		out = out[:maxRevisions]
	}

	return out, nil
}

// summarize turns a release into a Release.
func summarize(rel releasei.Releaser) (Release, error) {
	r, err := toV1(rel)
	if err != nil {
		return Release{}, err
	}

	out := Release{
		Name:         r.Name,
		Namespace:    r.Namespace,
		Revision:     r.Version,
		Status:       "",
		ChartVersion: "",
		Updated:      time.Time{},
		Description:  "",
	}

	if r.Info != nil {
		out.Status = r.Info.Status.String()
		out.Updated = r.Info.LastDeployed
		out.Description = r.Info.Description
	}

	if r.Chart != nil && r.Chart.Metadata != nil {
		out.ChartVersion = r.Chart.Metadata.Version
	}

	return out, nil
}

// toV1 unwraps the release type the SDK returns behind an interface.
func toV1(rel releasei.Releaser) (*releasev1.Release, error) {
	switch r := rel.(type) {
	case *releasev1.Release:
		return r, nil
	case releasev1.Release:
		return &r, nil
	default:
		return nil, fmt.Errorf("%w: %T", ErrReleaseType, rel)
	}
}

// newest returns the revision with the highest number. The storage drivers
// list revisions in their own order (the Secret driver by name, so v10 comes
// before v9), not by revision.
func newest(versions []releasei.Releaser) *releasev1.Release {
	var last *releasev1.Release

	for _, v := range versions {
		r, err := toV1(v)
		if err == nil && (last == nil || r.Version > last.Version) {
			last = r
		}
	}

	return last
}

// uninstalled reports whether a revision was uninstalled with its history
// kept.
func uninstalled(r *releasev1.Release) bool {
	return r != nil && r.Info != nil && r.Info.Status == common.StatusUninstalled
}

// SplitManifest splits a manifest into its documents, trimmed, empty ones
// dropped.
func SplitManifest(manifest string) []string {
	var docs []string

	for doc := range strings.SplitSeq(manifest, "\n---") {
		doc = strings.TrimSpace(strings.TrimPrefix(doc, "---"))
		if doc != "" {
			docs = append(docs, doc)
		}
	}

	return docs
}
