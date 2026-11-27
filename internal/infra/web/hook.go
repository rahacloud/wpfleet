package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/rahacloud/wpfleet/internal/domain/audit"
)

// syncTimeout bounds one sync of the clone.
const syncTimeout = 2 * time.Minute

// hook takes a push webhook: it checks the HMAC-SHA256 signature in the
// GitHub or the Gitea header and syncs the clone in the background.
func (s *Server) hook(c *echo.Context) error {
	if s.deps.VerifyWebhook == nil {
		return echo.ErrNotFound
	}

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return echo.ErrBadRequest
	}

	signature := c.Request().Header.Get("X-Hub-Signature-256")
	if signature == "" {
		signature = c.Request().Header.Get("X-Gitea-Signature")
	}

	if !s.deps.VerifyWebhook(body, signature) {
		s.log.WarnContext(c.Request().Context(), "a webhook with a wrong signature", "remote", c.RealIP())

		return c.String(http.StatusUnauthorized, "bad signature")
	}

	s.syncer.trigger()

	return c.String(http.StatusAccepted, "sync started")
}

// TriggerSync asks for a sync of the clone, as a push webhook does. The
// serve command's poll loop calls it, so polls and webhooks share one
// syncer and never sync at the same time. It must not be called after
// Shutdown.
func (s *Server) TriggerSync() {
	s.syncer.trigger()
}

// syncer syncs the clone in the background, one sync at a time: a webhook
// that arrives during a sync makes one more sync after it, however many
// arrive.
type syncer struct {
	repo    Repo
	log     *slog.Logger
	metrics *metrics
	wg      *sync.WaitGroup

	mu      sync.Mutex
	running bool
	again   bool
}

// newSyncer returns an idle syncer whose goroutines wg tracks.
func newSyncer(repo Repo, log *slog.Logger, m *metrics, wg *sync.WaitGroup) *syncer {
	return &syncer{repo: repo, log: log, metrics: m, wg: wg, mu: sync.Mutex{}, running: false, again: false}
}

// trigger asks for a sync.
func (y *syncer) trigger() {
	y.mu.Lock()
	defer y.mu.Unlock()

	if y.running {
		y.again = true

		return
	}

	y.running = true

	y.wg.Go(y.loop)
}

// loop syncs until no more syncs were asked for.
func (y *syncer) loop() {
	for {
		y.once()

		y.mu.Lock()
		if !y.again {
			y.running = false
			y.mu.Unlock()

			return
		}

		y.again = false
		y.mu.Unlock()
	}
}

// once runs one sync.
func (y *syncer) once() {
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	sha, err := y.repo.Sync(ctx)
	if err != nil {
		y.metrics.syncs.WithLabelValues(audit.ResultFailed).Inc()
		y.log.ErrorContext(ctx, "syncing the fleet repository", "error", err)

		return
	}

	y.metrics.syncs.WithLabelValues(audit.ResultOK).Inc()
	y.log.InfoContext(ctx, "synced the fleet repository", "commit", sha)
}
