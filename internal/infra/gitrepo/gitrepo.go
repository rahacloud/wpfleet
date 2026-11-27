// Package gitrepo keeps the server's clone of the fleet repository with
// go-git: it clones it, follows the remote branch with a fetch and a hard
// reset, and commits and pushes one change at a time. Nobody edits the clone
// by hand; whatever is in it that the remote does not hold is thrown away at
// the next Sync.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// remoteName is the only remote the clone has.
const remoteName = "origin"

// DefaultBranch is the branch followed when Config.Branch is empty.
const DefaultBranch = "main"

// ErrInvalid is returned for a Config that cannot work.
var ErrInvalid = errors.New("invalid git configuration")

// ErrOtherRemote is returned by Open for a clone whose origin is not
// Config.URL.
var ErrOtherRemote = errors.New("the clone's origin is another repository")

// Signature names a person or a bot in a commit.
type Signature struct {
	// Name is the display name.
	Name string
	// Email is the email address.
	Email string
}

// Config says which repository to clone, where, and how to authenticate.
type Config struct {
	// URL is the remote: an SSH (git@host:owner/repo.git or ssh://),
	// HTTPS or file:// URL.
	URL string
	// Branch is the branch followed and pushed to; DefaultBranch when
	// empty.
	Branch string
	// Dir is where the clone lives. An empty or missing directory is
	// cloned into; otherwise it must already be a clone of URL.
	Dir string
	// SSHKeyFile is a private key file for SSH URLs (a deploy key). Host
	// keys are checked against SSH_KNOWN_HOSTS, else ~/.ssh/known_hosts.
	SSHKeyFile string
	// Token is an access token for HTTPS URLs, sent with HTTP basic auth.
	// The user is the URL's, else x-access-token.
	Token string
	// Committer is the server's bot identity, the committer of every
	// commit it makes.
	Committer Signature
}

// Repo is a clone that follows one branch of its remote. Its methods are
// safe for concurrent use: Sync and Commit run one at a time, FileLog runs
// beside other reads.
type Repo struct {
	cfg  Config
	auth transport.AuthMethod
	repo *git.Repository

	// mu serializes changes of the clone (Sync, Commit) and lets reads
	// (FileLog, Head) share it.
	mu   sync.RWMutex
	head plumbing.Hash
}

// Open clones c.URL into c.Dir when c.Dir is empty or missing, else opens
// the clone there, and leaves it at the remote branch's head either way.
func Open(ctx context.Context, c Config) (*Repo, error) {
	if c.Branch == "" {
		c.Branch = DefaultBranch
	}

	err := c.validate()
	if err != nil {
		return nil, err
	}

	auth, err := authFor(c)
	if err != nil {
		return nil, err
	}

	empty, err := emptyDir(c.Dir)
	if err != nil {
		return nil, err
	}

	var repo *git.Repository
	if empty {
		repo, err = clone(ctx, c, auth)
	} else {
		repo, err = open(c)
	}

	if err != nil {
		return nil, err
	}

	r := &Repo{cfg: c, auth: auth, repo: repo, mu: sync.RWMutex{}, head: plumbing.ZeroHash}

	_, err = r.Sync(ctx)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// validate checks the fields Open needs.
func (c Config) validate() error {
	var errs []error

	if c.URL == "" {
		errs = append(errs, fmt.Errorf("%w: no repository URL", ErrInvalid))
	}

	if c.Dir == "" {
		errs = append(errs, fmt.Errorf("%w: no directory for the clone", ErrInvalid))
	}

	if c.Committer.Name == "" || c.Committer.Email == "" {
		errs = append(errs, fmt.Errorf("%w: the committer needs a name and an email", ErrInvalid))
	}

	if c.SSHKeyFile != "" && c.Token != "" {
		errs = append(errs, fmt.Errorf("%w: both an SSH key and a token are set; choose one", ErrInvalid))
	}

	if plumbing.NewBranchReferenceName(c.Branch).Validate() != nil {
		errs = append(errs, fmt.Errorf("%w: %q is not a branch name", ErrInvalid, c.Branch))
	}

	return errors.Join(errs...)
}

// emptyDir reports whether dir is missing or holds nothing.
func emptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading %s: %w", dir, err)
	}

	return len(entries) == 0, nil
}

// clone makes a single-branch clone of the configured branch.
func clone(ctx context.Context, c Config, auth transport.AuthMethod) (*git.Repository, error) {
	//nolint:exhaustruct_v5 // the rest of the options keep go-git's defaults: no depth, no submodules, no tags
	repo, err := git.PlainCloneContext(ctx, c.Dir, false, &git.CloneOptions{
		URL:           c.URL,
		Auth:          auth,
		RemoteName:    remoteName,
		ReferenceName: plumbing.NewBranchReferenceName(c.Branch),
		SingleBranch:  true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return nil, fmt.Errorf("cloning %s: %w", c.URL, err)
	}

	return repo, nil
}

// open opens an existing clone and checks that its origin is c.URL.
func open(c Config) (*git.Repository, error) {
	repo, err := git.PlainOpen(c.Dir)
	if err != nil {
		return nil, fmt.Errorf("opening the clone in %s: %w", c.Dir, err)
	}

	remote, err := repo.Remote(remoteName)
	if err != nil {
		return nil, fmt.Errorf("the clone in %s: %w", c.Dir, err)
	}

	urls := remote.Config().URLs
	if len(urls) == 0 || urls[0] != c.URL {
		return nil, fmt.Errorf("%w: %s holds a clone of %v, not %s", ErrOtherRemote, c.Dir, urls, c.URL)
	}

	return repo, nil
}

// Dir is the clone's directory, the fleet directory the operations read.
func (r *Repo) Dir() string {
	return r.cfg.Dir
}

// Head is the full SHA of the commit the clone is at.
func (r *Repo) Head() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.head.String()
}

// Sync fetches the branch and hard-resets the clone to it, removing every
// file the remote does not hold, and returns the new head's SHA.
func (r *Repo) Sync(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	err := r.sync(ctx)
	if err != nil {
		return "", err
	}

	return r.head.String(), nil
}

// sync is Sync with r.mu held.
func (r *Repo) sync(ctx context.Context) error {
	remote, err := r.fetch(ctx)
	if err != nil {
		return err
	}

	return r.resetTo(remote)
}

// fetch updates the remote-tracking branch and returns where it points.
func (r *Repo) fetch(ctx context.Context) (plumbing.Hash, error) {
	branch := plumbing.NewBranchReferenceName(r.cfg.Branch)
	tracking := plumbing.NewRemoteReferenceName(remoteName, r.cfg.Branch)
	spec := gitconfig.RefSpec(fmt.Sprintf("+%s:%s", branch, tracking))

	//nolint:exhaustruct_v5 // depth, tags and the progress writer keep go-git's defaults
	err := r.repo.FetchContext(ctx, &git.FetchOptions{
		RemoteName: remoteName,
		RefSpecs:   []gitconfig.RefSpec{spec},
		Auth:       r.auth,
		Tags:       git.NoTags,
		Force:      true,
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return plumbing.ZeroHash, fmt.Errorf("fetching %s from %s: %w", r.cfg.Branch, r.cfg.URL, err)
	}

	ref, err := r.repo.Reference(tracking, true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("reading %s after the fetch: %w", tracking, err)
	}

	return ref.Hash(), nil
}

// resetTo points the local branch and HEAD at hash and makes the worktree
// and the index match it exactly, untracked files removed.
func (r *Repo) resetTo(hash plumbing.Hash) error {
	branch := plumbing.NewBranchReferenceName(r.cfg.Branch)

	err := r.repo.Storer.SetReference(plumbing.NewHashReference(branch, hash))
	if err != nil {
		return fmt.Errorf("moving %s: %w", branch, err)
	}

	err = r.repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, branch))
	if err != nil {
		return fmt.Errorf("pointing HEAD at %s: %w", branch, err)
	}

	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("opening the worktree: %w", err)
	}

	//nolint:exhaustruct_v5 // no Files: the whole tree is reset
	err = wt.Reset(&git.ResetOptions{Commit: hash, Mode: git.HardReset})
	if err != nil {
		return fmt.Errorf("resetting the clone to %s: %w", hash, err)
	}

	err = wt.Clean(&git.CleanOptions{Dir: true})
	if err != nil {
		return fmt.Errorf("removing untracked files: %w", err)
	}

	r.head = hash

	return nil
}
