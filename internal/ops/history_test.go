package ops_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"

	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
	"github.com/rahacloud/wpfleet/internal/ops"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Names the history tests use again and again.
const (
	historySite   = "blog"
	historyChart  = "0.1.0"
	historyAuthor = "An Editor"
	deployed      = "deployed"
	superseded    = "superseded"
)

// fakeHistory is the revisions of each site's release, newest first.
type fakeHistory map[string][]helm.Release

func (f fakeHistory) History(site, _ string, maxRevisions int) ([]helm.Release, error) {
	list, ok := f[site]
	if !ok {
		return nil, helm.ErrReleaseNotFound
	}

	if maxRevisions > 0 && len(list) > maxRevisions {
		list = list[:maxRevisions]
	}

	return list, nil
}

// fakeAuthors knows the authors of some commits and counts the lookups.
type fakeAuthors struct {
	known   map[string]gitrepo.Signature
	lookups int
}

func (a *fakeAuthors) CommitAuthor(_ context.Context, sha string) (gitrepo.Signature, error) {
	a.lookups++

	sig, ok := a.known[sha]
	if !ok {
		return gitrepo.Signature{}, gitrepo.ErrUnknownCommit
	}

	return sig, nil
}

// revision is a stored revision with a description.
func revision(n int, status, description string) helm.Release {
	return helm.Release{
		Name: historySite, Namespace: "wp-staging", Revision: n, Status: status, ChartVersion: historyChart,
		Updated: time.Date(2026, 11, 20+n, 9, 0, 0, 0, time.UTC), Description: description,
	}
}

func TestHistory(t *testing.T) {
	t.Parallel()

	env := openStaging(t, t.TempDir())
	blog, err := env.Site(historySite)
	testutil.NoError(t, err)

	const (
		published = "1111111111111111111111111111111111111111"
		fromCLI   = "2222222222222222222222222222222222222222"
	)

	releases := fakeHistory{historySite: {
		revision(4, deployed, "wpfleet "+published),
		revision(3, superseded, "wpfleet "+fromCLI+"-dirty"),
		revision(2, superseded, "Upgrade complete"),
		revision(1, superseded, "wpfleet "+published),
	}}
	authors := &fakeAuthors{
		known:   map[string]gitrepo.Signature{published: {Name: historyAuthor, Email: "editor@example.com"}},
		lookups: 0,
	}

	got, err := ops.History(t.Context(), env, blog, 10, ops.HistoryOptions{Releases: releases, Authors: authors})
	testutil.NoError(t, err)
	testutil.DeepEqual(t, "history", got, []ops.Revision{
		{
			Revision: 4, Updated: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), Status: deployed,
			ChartVersion: historyChart, Commit: ops.Commit{SHA: published, Dirty: false}, Author: historyAuthor,
		},
		{
			Revision: 3, Updated: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC), Status: superseded,
			ChartVersion: historyChart, Commit: ops.Commit{SHA: fromCLI, Dirty: true}, Author: "",
		},
		{
			Revision: 2, Updated: time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC), Status: superseded,
			ChartVersion: historyChart, Commit: ops.Commit{SHA: "", Dirty: false}, Author: "",
		},
		{
			Revision: 1, Updated: time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC), Status: superseded,
			ChartVersion: historyChart, Commit: ops.Commit{SHA: published, Dirty: false}, Author: historyAuthor,
		},
	})
	testutil.Equal(t, "each commit is looked up once", authors.lookups, 2)

	limited, err := ops.History(t.Context(), env, blog, 1, ops.HistoryOptions{Releases: releases, Authors: authors})
	testutil.NoError(t, err)
	testutil.Equal(t, "limited", len(limited), 1)

	shop, err := env.Site("shop")
	testutil.NoError(t, err)

	none, err := ops.History(t.Context(), env, shop, 10, ops.HistoryOptions{Releases: releases, Authors: authors})
	testutil.NoError(t, err)
	testutil.Equal(t, "a site without a release has no history", len(none), 0)
}

// Without Authors the CLI finds them in the repository that holds the
// fleet, and outside one it finds none and does not fail.
func TestHistoryAuthorsFromTheFleetRepository(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	env := openStaging(t, dir)
	blog, err := env.Site(historySite)
	testutil.NoError(t, err)

	opts := func(sha string) ops.HistoryOptions {
		return ops.HistoryOptions{
			Releases: fakeHistory{historySite: {revision(1, deployed, "wpfleet "+sha)}},
			Authors:  nil,
		}
	}

	const unknown = "3333333333333333333333333333333333333333"

	got, err := ops.History(t.Context(), env, blog, 10, opts(unknown))
	testutil.NoError(t, err)
	testutil.Equal(t, "no repository, no author", got[0].Author, "")

	repo, err := git.PlainInit(dir, false)
	testutil.NoError(t, err)

	sha := commitAll(t, repo)

	got, err = ops.History(t.Context(), env, blog, 10, opts(sha))
	testutil.NoError(t, err)
	testutil.Equal(t, "the commit's author", got[0].Author, "Test")
	testutil.Equal(t, "the commit", got[0].Commit.SHA, sha)
}
