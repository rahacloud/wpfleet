package serve

import (
	"context"
	"log/slog"
	"net"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/infra/gitrepo"
	"github.com/rahacloud/wpfleet/internal/infra/oidc"
)

// SnapshotWait is how long a replaced snapshot stays.
const SnapshotWait = snapshotGrace

// AccessPolicy converts and validates the access rules.
func AccessPolicy(rules []config.AccessRule) (access.Policy, error) { return accessPolicy(rules) }

// CheckFleet checks the fleet in dir against the policy.
func CheckFleet(dir string, policy access.Policy) error { return checkFleet(dir, policy) }

// EventNote is the note of an Event.
func EventNote(r audit.Record) string { return eventNote(r) }

// Clone is the snapshotting fleet clone.
type Clone = clone

// Auditor is the audit writer.
type Auditor = auditor

// NewAuditor writes to log and records Events through cluster.
func NewAuditor(
	log *slog.Logger, cluster func(env string) (kubernetes.Interface, string, error), instance string,
) *Auditor {
	return newAuditor(log, cluster, instance)
}

// Run runs the server with the configuration file, the data directory and
// getenv; ready gets the listeners' addresses.
func Run(ctx context.Context, log *slog.Logger, config, data string, getenv func(string) string,
	ready func(listen, metrics net.Addr),
) error {
	return run(ctx, log, options{
		config: config, data: data, shutdownTimeout: time.Minute, getenv: getenv, version: "v0.0.0-test", ready: ready,
	})
}

// NewClone snapshots repo into root, on the clock now.
func NewClone(repo *gitrepo.Repo, root string, log *slog.Logger, now func() time.Time) (*Clone, error) {
	c, err := newClone(repo, root, log)
	if err != nil {
		return nil, err
	}

	c.now = now

	return c, nil
}

// Write writes one record.
func (a *auditor) Write(ctx context.Context, r audit.Record) { a.write(ctx, r) }

// Wait waits for the Events in flight.
func (a *auditor) Wait() { a.wait() }

// FilterGroups exchanges a code through the provider with the groups
// limited to those policy names.
func FilterGroups(
	ctx context.Context, p *oidc.Provider, policy access.Policy, code, verifier, nonce string,
) (access.Identity, error) {
	return ruleGroups(p, policy).Exchange(ctx, code, verifier, nonce)
}

// CheckBranding checks the brand's images in the fleet at dir.
func CheckBranding(dir string, b config.Branding) error {
	return checkBranding(dir, b)
}

// Hold takes the lock a Sync or a Commit holds through its fetch or push,
// and returns its release.
func (c *clone) Hold() func() {
	c.mu.Lock()

	return c.mu.Unlock
}
