package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-git/go-git/v5"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
)

// ReleaseHistory is what History needs from Helm. *helm.Client implements
// it.
type ReleaseHistory interface {
	// History returns the newest revisions of a site's release, newest
	// first.
	History(site, namespace string, maxRevisions int) ([]helm.Release, error)
}

// CommitAuthors finds who authored a fleet commit. *gitrepo.Repo, the
// server's clone, implements it.
type CommitAuthors interface {
	// CommitAuthor returns the author of the commit sha, a full hash, or an
	// error wrapping gitrepo.ErrUnknownCommit when the repository does not
	// have it.
	CommitAuthor(ctx context.Context, sha string) (gitrepo.Signature, error)
}

// Revision is one revision of a site's release.
type Revision struct {
	// Revision is Helm's revision number.
	Revision int `json:"revision"`
	// Updated is when the revision was deployed.
	Updated time.Time `json:"updated"`
	// Status is Helm's status: deployed, superseded, failed and the like.
	Status string `json:"status"`
	// ChartVersion is the version of the chart it was rendered from.
	ChartVersion string `json:"chartVersion"`
	// Commit is the fleet commit a wpfleet deploy recorded; zero for a
	// revision that wpfleet did not deploy or that recorded no commit.
	Commit Commit `json:"commit"`
	// Author is the Git author of Commit, for a publish from the UI the
	// person who made it; empty when the repository does not have it.
	Author string `json:"author,omitempty"`
}

// HistoryOptions are the options of History.
type HistoryOptions struct {
	// Releases reads the release's revisions; nil uses the environment's
	// Helm client.
	Releases ReleaseHistory
	// Authors finds the authors of the commits; nil looks them up in the
	// Git repository that holds the fleet directory, and finds none when
	// there is no repository there.
	Authors CommitAuthors
}

// History returns the newest n revisions of site's release, newest first,
// each with the commit its description records (ParseDescription) and that
// commit's Git author. A site without a release has no revisions and no
// error. A commit whose author cannot be read only leaves Author empty: a
// revision deployed from an unpushed tree or another branch is still
// history.
func History(ctx context.Context, env *Env, site fleet.Site, n int, opts HistoryOptions) ([]Revision, error) {
	releases := opts.Releases
	if releases == nil {
		h, err := env.Helm()
		if err != nil {
			return nil, err
		}

		releases = h
	}

	list, err := releases.History(site.Name, env.Env.Namespace, n)

	switch {
	case errors.Is(err, helm.ErrReleaseNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	authors := opts.Authors
	if authors == nil {
		authors = localAuthors(env.Dir)
	}

	revisions := make([]Revision, 0, len(list))
	names := map[string]string{}

	for _, r := range list {
		rev := Revision{
			Revision: r.Revision, Updated: r.Updated, Status: r.Status, ChartVersion: r.ChartVersion,
			Commit: Commit{SHA: "", Dirty: false}, Author: "",
		}

		if commit, ok := ParseDescription(r.Description); ok {
			rev.Commit = commit
			rev.Author = authorOf(ctx, authors, names, commit.SHA)
		}

		revisions = append(revisions, rev)
	}

	return revisions, nil
}

// authorOf is the author's name of sha, looked up once per History call.
func authorOf(ctx context.Context, authors CommitAuthors, names map[string]string, sha string) string {
	if name, ok := names[sha]; ok {
		return name
	}

	name := ""

	sig, err := authors.CommitAuthor(ctx, sha)

	switch {
	case err == nil:
		name = sig.Name
		if name == "" {
			name = sig.Email
		}
	case !errors.Is(err, gitrepo.ErrUnknownCommit):
		slog.Warn("cannot read a commit's author", "commit", sha, "err", err)
	}

	names[sha] = name

	return name
}

// repoAuthors finds authors in a repository opened with go-git; a nil
// repository knows no commit.
type repoAuthors struct {
	repo *git.Repository
}

// CommitAuthor looks sha up in the repository.
func (a repoAuthors) CommitAuthor(_ context.Context, sha string) (gitrepo.Signature, error) {
	if a.repo == nil {
		return gitrepo.Signature{}, fmt.Errorf("%w: no repository", gitrepo.ErrUnknownCommit)
	}

	return gitrepo.Author(a.repo, sha)
}

// localAuthors opens the Git repository that holds dir, as FleetCommit
// does; outside a repository it knows no commit.
func localAuthors(dir string) repoAuthors {
	//nolint:exhaustruct_v5 // the common-dir support is off, as in git's default layout
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return repoAuthors{repo: nil}
	}

	return repoAuthors{repo: repo}
}
