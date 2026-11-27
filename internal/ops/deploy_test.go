package ops_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// errFake is what the fake Releases fails with.
var errFake = errors.New("fake failure")

// recordingReleases deploys nothing and records each install's options.
type recordingReleases struct {
	opts map[string]helm.InstallOptions
	fail string
}

func (r *recordingReleases) Render(context.Context, string, string, []string) (string, error) {
	return "", nil
}

func (r *recordingReleases) UpgradeInstall(
	_ context.Context, site, namespace string, _ []string, opts helm.InstallOptions,
) (helm.Release, error) {
	if site == r.fail {
		return helm.Release{}, errFake
	}

	r.opts[site] = opts

	//nolint:exhaustruct_v5 // the fields a deploy result reads
	return helm.Release{Name: site, Namespace: namespace, Revision: 1, Status: "deployed"}, nil
}

func (r *recordingReleases) LiveManifest(string, string) (string, error) {
	return "", helm.ErrReleaseNotFound
}

// openStaging writes a fleet with blog and shop in staging, and opens it.
func openStaging(t *testing.T, dir string) *ops.Env {
	t.Helper()

	testutil.WriteFile(t, dir, "wpfleet.yaml",
		"environments:\n  - name: staging\n    namespace: wp-staging\n    kubeconfig: kubeconfig\n")
	testutil.WriteFile(t, dir, "kubeconfig", `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://k8s.example.com:6443"}}]
users: [{name: u, user: {token: not-a-real-token}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`)
	testutil.WriteFile(t, dir, "environments/staging/blog.yaml", "site: blog\n")
	testutil.WriteFile(t, dir, "environments/staging/shop.yaml", "site: shop\n")

	f, err := ops.LoadFleet(dir)
	testutil.NoError(t, err)

	env, err := f.Open("staging")
	testutil.NoError(t, err)

	return env
}

func TestDeployRecordsTheCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := openStaging(t, dir)

	repo, err := git.PlainInit(dir, false)
	testutil.NoError(t, err)

	sha := commitAll(t, repo)
	releases := &recordingReleases{opts: map[string]helm.InstallOptions{}, fail: "shop"}

	var events []ops.Event

	opts := ops.DeployOptions{Wait: true, Releases: releases, Commit: nil}

	report, err := ops.Deploy(t.Context(), env, env.Sites, opts, func(e ops.Event) { events = append(events, e) })
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "options", releases.opts["blog"], helm.InstallOptions{Wait: true, Description: "wpfleet " + sha})
	testutil.Equal(t, "report commit", report.Commit.SHA, sha)
	testutil.DeepEqual(t, "failed", report.Failed, []string{"shop"})
	testutil.ErrorIs(t, report.Err(), ops.ErrSitesFailed)
	testutil.Equal(t, "failed status", report.Results[1].Status, ops.StatusFailed)

	testutil.Equal(t, "events", len(events), 2)
	testutil.DeepEqual(t, "first event", events[0], ops.Event{
		Kind: ops.EventDeploying, Env: "staging", Site: "blog", Job: "", Namespace: "", Text: "deploying blog to staging",
	})

	// A commit the caller names wins: the server deploys its clone's head.
	given := ops.Commit{SHA: "0123456789abcdef0123456789abcdef01234567", Dirty: false}
	opts.Commit = &given

	_, err = ops.Deploy(t.Context(), env, env.Sites[:1], opts, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, "given commit", releases.opts["blog"].Description, "wpfleet "+given.SHA)
}

func TestDeployStopsWithTheContext(t *testing.T) {
	t.Parallel()

	env := openStaging(t, t.TempDir())
	releases := &recordingReleases{opts: map[string]helm.InstallOptions{}, fail: ""}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	report, err := ops.Deploy(ctx, env, env.Sites, ops.DeployOptions{Wait: false, Releases: releases, Commit: nil}, nil)
	testutil.ErrorIs(t, err, context.Canceled)
	testutil.Equal(t, "nothing deployed", len(report.Results)+len(releases.opts), 0)
}
