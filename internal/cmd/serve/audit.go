package serve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/web/ui"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// The Kubernetes Event wpfleet serve records.
const (
	// eventController is the Event's reportingController.
	eventController = "wpfleet"
	// eventTimeout bounds the recording of one Event.
	eventTimeout = 10 * time.Second
	// maxEventNote is the longest note the API server accepts, in bytes.
	maxEventNote = 1024
)

// eventAction reports whether an action puts an Event on the site's
// Deployment. A promote deploys, so it gets one too.
func eventAction(action string) bool {
	switch ui.Action(action) {
	case ui.ActionDeploy, ui.ActionPromote, ui.ActionLogin, ui.ActionBackup, ui.ActionRestore:
		return true
	default:
		return false
	}
}

// clusterFor returns the clientset and namespace of an environment of the
// fleet as the clone holds it now.
type clusterFor func(env string) (kubernetes.Interface, string, error)

// auditor writes every audit record as one JSON line and, for an allowed
// action that ran on a site, records a Kubernetes Event on its Deployment so
// kubectl describe shows it. Events are recorded in the background; wait
// blocks until they are written, and after it no Event is started, though
// the line is still written.
type auditor struct {
	log      *slog.Logger
	cluster  clusterFor
	instance string

	// mu guards closed and orders every wg.Add before wait's wg.Wait, which
	// sync.WaitGroup requires.
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// newAuditor writes to log and records Events through cluster, as instance
// (the pod's name).
func newAuditor(log *slog.Logger, cluster clusterFor, instance string) *auditor {
	return &auditor{log: log, cluster: cluster, instance: instance, mu: sync.Mutex{}, closed: false, wg: sync.WaitGroup{}}
}

// write is the web.Auditor.
func (a *auditor) write(ctx context.Context, r audit.Record) {
	a.log.LogAttrs(ctx, slog.LevelInfo, audit.Message, r.Attrs()...)

	if !r.Allowed || !eventAction(r.Action) || (r.Result != audit.ResultOK && r.Result != audit.ResultFailed) {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		a.log.WarnContext(ctx, "not recording a Kubernetes Event: the server is stopping", "env", r.Env, "site", r.Site)

		return
	}

	a.wg.Go(func() {
		ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), eventTimeout)
		defer cancel()

		err := a.event(ectx, r)
		if err != nil {
			a.log.WarnContext(ectx, "recording a Kubernetes Event", "env", r.Env, "site", r.Site, "error", err)
		}
	})
}

// wait blocks until the Events in flight are recorded and refuses new ones.
func (a *auditor) wait() {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()

	a.wg.Wait()
}

// event records r on the Deployment of its site.
func (a *auditor) event(ctx context.Context, r audit.Record) error {
	cs, namespace, err := a.cluster(r.Env)
	if err != nil {
		return err
	}

	//nolint:exhaustruct_v5 // a selector and a limit are the only options a lookup needs
	deployments, err := cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: kube.SiteSelector(r.Site), Limit: 1,
	})
	if err != nil {
		return fmt.Errorf("finding the Deployment of %s: %w", r.Site, err)
	}

	if len(deployments.Items) == 0 {
		return nil
	}

	event := siteEvent(r, deployments.Items[0].ObjectMeta, a.instance)

	//nolint:exhaustruct_v5 // a plain create needs no options
	_, err = cs.EventsV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating the Event: %w", err)
	}

	return nil
}

// siteEvent is the Event of r on the Deployment that deployment describes.
func siteEvent(r audit.Record, deployment metav1.ObjectMeta, instance string) *eventsv1.Event {
	kind, reason := corev1.EventTypeNormal, "Wpfleet"+title(r.Action)
	if r.Result == audit.ResultFailed {
		kind, reason = corev1.EventTypeWarning, reason+"Failed"
	}

	//nolint:exhaustruct_v5 // an Event sets what it reports; the rest is the API server's
	return &eventsv1.Event{
		// The API server checks generateName as a name prefix, which may end
		// in a dash but not in a dot.
		GenerateName:        deployment.Name + ".wpfleet-",
		Namespace:           deployment.Namespace,
		EventTime:           metav1.NewMicroTime(r.Time),
		ReportingController: eventController,
		ReportingInstance:   instance,
		Action:              title(r.Action),
		Reason:              reason,
		Type:                kind,
		Note:                eventNote(r),
		Regarding: corev1.ObjectReference{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
			Namespace:  deployment.Namespace,
			Name:       deployment.Name,
			UID:        deployment.UID,
		},
	}
}

// eventNote says who did what, in the API server's size limit.
func eventNote(r audit.Record) string {
	var b strings.Builder

	b.WriteString(r.Action)
	b.WriteString(" by ")
	b.WriteString(r.Actor.Email)

	if r.Commit != "" {
		b.WriteString(" at commit ")
		b.WriteString(r.Commit)
	}

	if r.WPUser != "" {
		b.WriteString(" as WordPress user ")
		b.WriteString(r.WPUser)
	}

	b.WriteString(": ")
	b.WriteString(r.Result)

	if r.Detail != "" {
		b.WriteString(", ")
		b.WriteString(r.Detail)
	}

	note := b.String()
	if len(note) > maxEventNote {
		note = strings.ToValidUTF8(note[:maxEventNote], "")
	}

	return note
}

// title capitalizes an action's name, which is lowercase ASCII.
func title(action string) string {
	if action == "" {
		return action
	}

	return strings.ToUpper(action[:1]) + action[1:]
}

// fleetCluster is the clusterFor of the fleet in the directory dir returns.
func fleetCluster(dir func() string) clusterFor {
	return func(name string) (kubernetes.Interface, string, error) {
		f, err := ops.LoadFleet(dir())
		if err != nil {
			return nil, "", err
		}

		env, err := f.Open(name)
		if err != nil {
			return nil, "", err
		}

		c, ok := env.Kube.(ops.Cluster)
		if !ok {
			return nil, "", fmt.Errorf("environment %s: %w", name, ops.ErrNoClientset)
		}

		return c.Clientset(), env.Env.Namespace, nil
	}
}
