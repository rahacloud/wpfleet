package backup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// drainTimeout bounds how long Follow waits for the logs after the Job has
// finished, in case a stream hangs.
const drainTimeout = 30 * time.Second

// Follow waits for the Job and meanwhile streams the logs of its pods into w:
// each pod's containers in the given order (init containers first), every
// pod the Job makes, oldest first. A container that has not started yet is
// retried every poll until it starts or the Job ends. It returns the Job's
// result: an error wrapping kube.ErrJobFailed when the Job failed.
func Follow(ctx context.Context, k kube.Jobs, job string, containers []string, w io.Writer, poll time.Duration) error {
	logCtx, stop := context.WithCancel(ctx)
	defer stop()

	f := &follower{k: k, job: job, containers: containers, w: w, poll: poll, done: atomic.Bool{}, seen: map[string]bool{}}

	logsDone := make(chan struct{})

	go func() {
		defer close(logsDone)

		f.run(logCtx)
	}()

	err := k.WaitJob(ctx, job)

	f.done.Store(true)

	select {
	case <-logsDone:
	case <-time.After(drainTimeout):
		slog.Warn("stopped following logs that did not end", "job", job)
	}

	stop()
	<-logsDone

	return err
}

// follower streams the logs of one Job's pods.
type follower struct {
	k          kube.Jobs
	job        string
	containers []string
	w          io.Writer
	poll       time.Duration
	// done is set once the Job has finished: no new pod or container will
	// start, so waiting for one ends.
	done atomic.Bool
	seen map[string]bool
}

// run follows pod after pod until the Job is done and every pod it made has
// been followed, or ctx ends.
func (f *follower) run(ctx context.Context) {
	for {
		pod, ok := f.nextPod(ctx)
		if !ok {
			if f.done.Load() || !sleep(ctx, f.poll) {
				return
			}

			continue
		}

		f.seen[pod] = true

		for _, c := range f.containers {
			if !f.stream(ctx, pod, c) {
				break
			}
		}
	}
}

// nextPod returns the oldest pod of the Job not followed yet.
func (f *follower) nextPod(ctx context.Context) (string, bool) {
	pods, err := f.k.JobPods(ctx, f.job)
	if err != nil {
		slog.Debug("listing the Job's pods", "job", f.job, "err", err)

		return "", false
	}

	for _, p := range pods {
		if !f.seen[p.Name] {
			return p.Name, true
		}
	}

	return "", false
}

// stream copies one container's log into w, retrying while the container has
// not started. It returns false when it gave up because the Job is done or
// ctx ended, so the pod's later containers never ran.
func (f *follower) stream(ctx context.Context, pod, container string) bool {
	_, _ = fmt.Fprintf(f.w, "==> %s/%s <==\n", pod, container)

	for {
		err := f.k.StreamLogs(ctx, pod, container, f.w)
		if err == nil {
			return true
		}

		slog.Debug("waiting for the container's log", "pod", pod, "container", container, "err", err)

		if f.done.Load() || !sleep(ctx, f.poll) {
			return false
		}
	}
}

// sleep waits for d, and returns false when ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
