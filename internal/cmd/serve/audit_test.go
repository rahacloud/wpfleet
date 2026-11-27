package serve_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/rahacloud/wpfleet/internal/cmd/serve"
	"github.com/rahacloud/wpfleet/internal/domain/access"
	"github.com/rahacloud/wpfleet/internal/domain/audit"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const namespace = "wp-staging"

func newFakeCluster() *fake.Clientset {
	//nolint:exhaustruct_v5 // a Deployment with the labels the site's pods carry
	return fake.NewClientset(&appsv1.Deployment{
		Name: siteBlog, Namespace: namespace, UID: "uid-blog",
		Labels: map[string]string{kube.LabelInstance: siteBlog},
	})
}

func record(action, result string, allowed bool) audit.Record {
	return audit.Record{
		Time: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC),
		Actor: access.Identity{
			Subject: "u1", Email: "editor@example.com", Name: "An Editor", Groups: []string{groupPlatform},
		},
		Action: action, Env: envStaging, Site: siteBlog, Commit: "", Result: result, Detail: "", WPUser: "",
		Allowed: allowed,
	}
}

func listEvents(t *testing.T, cs kubernetes.Interface) (*eventsv1.EventList, error) {
	t.Helper()

	//nolint:exhaustruct_v5 // list them all
	events, err := cs.EventsV1().Events(namespace).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
	}

	return events, nil
}

func newTestAuditor(cs kubernetes.Interface, out *bytes.Buffer) *serve.Auditor {
	log := slog.New(slog.NewJSONHandler(out, nil))

	cluster := func(string) (kubernetes.Interface, string, error) { return cs, namespace, nil }

	return serve.NewAuditor(log, cluster, "wpfleet-0")
}

func TestAuditWritesAJSONLineAndAnEvent(t *testing.T) {
	t.Parallel()

	cs := newFakeCluster()

	var out bytes.Buffer

	a := newTestAuditor(cs, &out)

	rec := record("login", audit.ResultOK, true)
	rec.WPUser = "editor"
	a.Write(t.Context(), rec)
	a.Wait()

	var line map[string]json.RawMessage

	testutil.NoError(t, json.Unmarshal(out.Bytes(), &line))
	testutil.Equal(t, "audit flag", string(line["audit"]), "true")
	testutil.Equal(t, "action", string(line["action"]), `"login"`)

	events, err := listEvents(t, cs)
	testutil.NoError(t, err)
	testutil.Equal(t, "events", len(events.Items), 1)

	e := events.Items[0]
	testutil.Equal(t, "regarding", e.Regarding.Name, siteBlog)
	testutil.Equal(t, "regarding uid", string(e.Regarding.UID), "uid-blog")
	testutil.Equal(t, "kind", e.Regarding.Kind, "Deployment")
	testutil.Equal(t, "reason", e.Reason, "WpfleetLogin")
	testutil.Equal(t, "type", e.Type, corev1.EventTypeNormal)
	testutil.Equal(t, "controller", e.ReportingController, "wpfleet")
	testutil.Equal(t, "instance", e.ReportingInstance, "wpfleet-0")
	testutil.Contains(t, "note", e.Note, "editor@example.com", "WordPress user editor")
}

func TestAuditEventForAFailure(t *testing.T) {
	t.Parallel()

	cs := newFakeCluster()
	a := newTestAuditor(cs, &bytes.Buffer{})

	a.Write(t.Context(), record("deploy", audit.ResultFailed, true))
	a.Wait()

	events, err := listEvents(t, cs)
	testutil.NoError(t, err)
	testutil.Equal(t, "events", len(events.Items), 1)
	testutil.Equal(t, "reason", events.Items[0].Reason, "WpfleetDeployFailed")
	testutil.Equal(t, "type", events.Items[0].Type, corev1.EventTypeWarning)
}

func TestAuditNoEventWithoutAnAction(t *testing.T) {
	t.Parallel()

	for name, rec := range map[string]audit.Record{
		"denied":   record("deploy", audit.ResultDenied, false),
		"busy":     record("backup", audit.ResultBusy, true),
		"unknown":  record("view", audit.ResultOK, true),
		"no event": record("restore", audit.ResultDenied, true),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cs := newFakeCluster()

			var out bytes.Buffer

			a := newTestAuditor(cs, &out)
			a.Write(t.Context(), rec)
			a.Wait()

			testutil.Contains(t, "audit line", out.String(), `"audit":true`)

			events, err := listEvents(t, cs)
			testutil.NoError(t, err)
			testutil.Equal(t, "events", len(events.Items), 0)
		})
	}
}

func TestAuditWithoutTheDeployment(t *testing.T) {
	t.Parallel()

	cs := fake.NewClientset()
	a := newTestAuditor(cs, &bytes.Buffer{})

	a.Write(t.Context(), record("backup", audit.ResultOK, true))
	a.Wait()

	events, err := listEvents(t, cs)
	testutil.NoError(t, err)
	testutil.Equal(t, "events", len(events.Items), 0)
}

func TestEventNoteFitsTheLimit(t *testing.T) {
	t.Parallel()

	rec := record("restore", audit.ResultFailed, true)
	rec.Detail = strings.Repeat("ü", 2000)

	note := serve.EventNote(rec)
	testutil.True(t, "at most 1024 bytes", len(note) <= 1024)
	testutil.True(t, "valid UTF-8", strings.ToValidUTF8(note, "?") == note)
	testutil.Contains(t, "starts with who", note, "restore by editor@example.com: failed")
}

// Once the server waits for the Events in flight, a late record is still
// written as a line but starts no Event, which would race the wait.
func TestAuditAfterWaitWritesOnlyTheLine(t *testing.T) {
	t.Parallel()

	cs := newFakeCluster()

	var out bytes.Buffer

	a := newTestAuditor(cs, &out)
	a.Wait()
	a.Write(t.Context(), record("deploy", audit.ResultFailed, true))
	a.Wait()

	testutil.Contains(t, "audit line", out.String(), `"audit":true`, "the server is stopping")

	events, err := listEvents(t, cs)
	testutil.NoError(t, err)
	testutil.Equal(t, "events", len(events.Items), 0)
}
