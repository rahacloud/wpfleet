package kube_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const (
	ns     = "wp-staging"
	blog   = "blog"
	pod    = "blog-1"
	cronJb = "blog-backup"
	phar   = "/tmp/wp-cli.phar"
)

func TestSitePodsAndRunningPod(t *testing.T) {
	t.Parallel()

	old := kubetest.RunningPod(ns, blog, "blog-old")
	old.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))

	newer := kubetest.RunningPod(ns, blog, "blog-new")

	notReady := kubetest.RunningPod(ns, blog, "blog-starting")
	notReady.Status.Conditions = nil
	notReady.CreationTimestamp = metav1.NewTime(time.Now().Add(time.Hour))

	backup := kubetest.RunningPod(ns, blog, "blog-backup-1")
	backup.Labels[kube.LabelComponent] = kube.ComponentBackup

	k := kubetest.New(ns, old, newer, notReady, backup, kubetest.RunningPod(ns, "shop", "shop-1"))
	ctx := t.Context()

	pods, err := k.SitePods(ctx, blog)
	testutil.NoError(t, err)

	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Name)
	}

	testutil.DeepEqual(t, "SitePods", names, []string{"blog-new", "blog-old", "blog-starting"})

	running, err := k.RunningPod(ctx, blog)
	testutil.NoError(t, err)
	testutil.Equal(t, "RunningPod", running.Name, "blog-new")

	_, err = k.RunningPod(ctx, "news")
	testutil.ErrorIs(t, err, kube.ErrNoRunningPod)

	testutil.Equal(t, "SiteSelector", kube.SiteSelector(blog),
		"app.kubernetes.io/instance=blog,app.kubernetes.io/component!=backup")
}

func TestServing(t *testing.T) {
	t.Parallel()

	p := kubetest.RunningPod(ns, blog, pod)
	testutil.True(t, "serving", kube.Serving(*p))

	other := p.DeepCopy()
	other.Spec.Containers[0].Name = "sidecar"
	testutil.True(t, "no WordPress container", !kube.Serving(*other))

	deleting := p.DeepCopy()
	deleting.DeletionTimestamp = new(metav1.Now())
	testutil.True(t, "being deleted", !kube.Serving(*deleting))

	pending := p.DeepCopy()
	pending.Status.Phase = corev1.PodPending
	testutil.True(t, "pending", !kube.Serving(*pending))
}

// echo answers every command with "ran " and its stdin, and fails "false"
// with exit code 3.
func echo(call kubetest.Call, stdout, _ io.Writer) error {
	if call.Command[0] == "false" {
		return kubetest.Exit(3)
	}

	_, err := stdout.Write(append([]byte("ran "), call.Stdin...))
	if err != nil {
		return fmt.Errorf("writing stdout: %w", err)
	}

	return nil
}

func TestExec(t *testing.T) {
	t.Parallel()

	k := kubetest.New(ns)
	k.Handle(echo)

	ctx := t.Context()

	var out bytes.Buffer

	err := k.Exec(ctx, pod, kube.WordPressContainer, []string{"cat"}, strings.NewReader("hello"), &out, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, "stdout", out.String(), "ran hello")

	err = k.Exec(ctx, pod, "", []string{"false"}, nil, nil, nil)
	code, ok := kube.ExitCode(err)
	testutil.True(t, "exit error", ok)
	testutil.Equal(t, "exit code", code, 3)

	_, ok = kube.ExitCode(context.Canceled)
	testutil.True(t, "a plain error has no exit code", !ok)
}

func TestCopyFile(t *testing.T) {
	t.Parallel()

	k := kubetest.New(ns)
	ctx := t.Context()

	local := testutil.WriteFile(t, t.TempDir(), "wp-cli.phar", "<?php phar")

	testutil.NoError(t, k.CopyFile(ctx, pod, local, phar))

	data, ok := k.File(pod, phar)
	testutil.True(t, "copied", ok)
	testutil.Equal(t, "content", string(data), "<?php phar")
	testutil.DeepEqual(t, "files", k.Files(), []string{pod + ":" + phar})

	calls := k.Calls()
	last := calls[len(calls)-1]
	testutil.Equal(t, "container", last.Container, kube.WordPressContainer)
	testutil.True(t, "extracts into /tmp", slices.Contains(last.Command, "/tmp"))

	testutil.NoError(t, k.Exec(ctx, pod, "", []string{"test", "-f", phar}, nil, nil, nil))
	testutil.ErrorIs(t, k.CopyFile(ctx, pod, local, "relative/path"), kube.ErrRelativePath)
	testutil.ErrorIs(t, k.CopyFile(ctx, pod, filepath.Join(t.TempDir(), "missing"), "/tmp/x"), os.ErrNotExist)
}

func backupCronJob() *batchv1.CronJob {
	//nolint:exhaustruct_v5 // a test CronJob needs only its template
	return &batchv1.CronJob{
		Name:      cronJb,
		Namespace: ns,
		UID:       "uid-1",
		Spec: batchv1.CronJobSpec{
			Schedule: "0 3 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Labels: map[string]string{kube.LabelInstance: blog},
				Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: "upload", Image: "rclone"}},
				}}},
			},
		},
	}
}

func TestJobFromCronJob(t *testing.T) {
	t.Parallel()

	k := kubetest.New(ns, backupCronJob())
	ctx := t.Context()

	job, err := k.JobFromCronJob(ctx, cronJb, "blog-backup-manual-1")
	testutil.NoError(t, err)

	testutil.Equal(t, "name", job.Name, "blog-backup-manual-1")
	testutil.Equal(t, "annotation", job.Annotations["cronjob.kubernetes.io/instantiate"], "manual")
	testutil.Equal(t, "label", job.Labels[kube.LabelInstance], blog)
	testutil.Equal(t, "owner", string(job.OwnerReferences[0].UID), "uid-1")
	testutil.Equal(t, "container", job.Spec.Template.Spec.Containers[0].Name, "upload")

	_, err = k.JobFromCronJob(ctx, "missing", "x")
	testutil.Error(t, err, "missing CronJob")
}

// waitFor starts a Job, gives it the condition (none when empty) and waits
// for it briefly.
func waitFor(t *testing.T, k *kubetest.Fake, name string, cond batchv1.JobConditionType) error {
	t.Helper()

	ctx := t.Context()

	job, err := k.JobFromCronJob(ctx, cronJb, name)
	testutil.NoError(t, err)

	if cond != "" {
		testutil.NoError(t, k.SetJobCondition(ctx, job.Name, cond))
	}

	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	return k.WaitJob(short, job.Name)
}

func TestWaitJob(t *testing.T) {
	t.Parallel()

	k := kubetest.New(ns, backupCronJob())

	testutil.NoError(t, waitFor(t, k, "manual-1", batchv1.JobComplete))
	testutil.ErrorIs(t, waitFor(t, k, "manual-2", batchv1.JobFailed), kube.ErrJobFailed)
	testutil.ErrorIs(t, waitFor(t, k, "manual-3", ""), context.DeadlineExceeded)
	testutil.Error(t, k.WaitJob(t.Context(), "missing"), "missing Job")
}

func TestJobDone(t *testing.T) {
	t.Parallel()

	//nolint:exhaustruct_v5 // only the conditions matter here
	job := batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{
		{Type: batchv1.JobSuccessCriteriaMet, Status: corev1.ConditionTrue},
		{Type: batchv1.JobFailed, Status: corev1.ConditionFalse},
	}}}

	done, err := kube.JobDone(job)
	testutil.True(t, "not done yet", !done && err == nil)
}

func TestJobPodsAndLogs(t *testing.T) {
	t.Parallel()

	jobPod := kubetest.RunningPod(ns, blog, "blog-backup-manual-1-abcde")
	jobPod.Labels["batch.kubernetes.io/job-name"] = "blog-backup-manual-1"

	k := kubetest.New(ns, jobPod, kubetest.RunningPod(ns, blog, pod))
	ctx := t.Context()

	pods, err := k.JobPods(ctx, "blog-backup-manual-1")
	testutil.NoError(t, err)
	testutil.Equal(t, "job pods", len(pods), 1)
	testutil.Equal(t, "job pod", pods[0].Name, jobPod.Name)

	var logs bytes.Buffer

	testutil.NoError(t, k.StreamLogs(ctx, jobPod.Name, "upload", &logs))

	// The fake clientset answers every log request with this text.
	testutil.Equal(t, "logs", logs.String(), "fake logs")
}

const kubeconfig = `apiVersion: v1
kind: Config
clusters:
  - name: a
    cluster: {server: "https://a.example.com:6443"}
  - name: b
    cluster: {server: "https://b.example.com:6443"}
users:
  - name: u
    user: {token: not-a-real-token}
contexts:
  - name: a
    context: {cluster: a, user: u, namespace: default}
  - name: b
    context: {cluster: b, user: u}
current-context: a
`

func TestTarget(t *testing.T) {
	t.Parallel()

	path := testutil.WriteFile(t, t.TempDir(), "config", kubeconfig)

	target := kube.TargetFor(fleet.Environment{
		Name: "staging", Namespace: ns, Kubeconfig: path, Context: "b", ValuesDir: "", PromoteFrom: "",
	})

	rest, err := target.ClientConfig().ClientConfig()
	testutil.NoError(t, err)
	testutil.Equal(t, "host", rest.Host, "https://b.example.com:6443")

	namespace, _, err := target.ClientConfig().Namespace()
	testutil.NoError(t, err)
	testutil.Equal(t, "namespace", namespace, ns)

	getterConfig, err := target.RESTClientGetter().ToRESTConfig()
	testutil.NoError(t, err)
	testutil.Equal(t, "getter host", getterConfig.Host, "https://b.example.com:6443")

	client, err := kube.New(target)
	testutil.NoError(t, err)
	testutil.Equal(t, "client namespace", client.Namespace(), ns)
	testutil.True(t, "clientset", client.Clientset() != nil)

	_, err = kube.New(kube.Target{Kubeconfig: path, Context: "", Namespace: ""})
	testutil.ErrorIs(t, err, kube.ErrNoNamespace)

	_, err = kube.New(kube.Target{Kubeconfig: path, Context: "missing", Namespace: ns})
	testutil.True(t, "unknown context", err != nil && !errors.Is(err, kube.ErrNoNamespace))
}
