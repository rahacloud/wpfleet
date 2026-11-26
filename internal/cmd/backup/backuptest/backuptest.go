// Package backuptest runs the backup and copy commands against a fleet in a
// temporary directory, a fake S3 service and one kubetest cluster per
// environment, in which every Job finishes as soon as it is created and gets
// one pod whose logs the fake clientset serves.
package backuptest

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/rahacloud/wpfleet/internal/cmd/backup"
	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/infra/kube"
	"github.com/rahacloud/wpfleet/internal/infra/kube/kubetest"
	"github.com/rahacloud/wpfleet/internal/infra/s3"
	"github.com/rahacloud/wpfleet/internal/infra/s3/s3test"
	"github.com/rahacloud/wpfleet/internal/infra/wpcli"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

// Bucket is the fleet's backup bucket.
const Bucket = "wordpress-backups"

// Password is the database password of every test site; no output may hold
// it.
const Password = "Secretpassword1234567890"

// Environments are the test fleet's environments.
const (
	Development = "development"
	Staging     = "staging"
	Production  = "production"
)

// Now is the time the commands see.
func Now() time.Time {
	return time.Date(2026, 11, 26, 10, 15, 0, 0, time.UTC)
}

// Namespace is the namespace of a test environment.
func Namespace(env string) string {
	return "wp-" + env
}

// pollInterval keeps the commands' polling quick.
const pollInterval = 5 * time.Millisecond

// Harness is a fleet with its fakes.
type Harness struct {
	// Dir is the fleet directory.
	Dir string
	// S3 is the fake bucket service.
	S3 *s3test.Server
	// Kube holds the cluster of each environment.
	Kube map[string]*kubetest.Fake
	// Deps are the commands' dependencies, pointing at the fakes.
	Deps backup.Deps
	// Out and Err are what the last Run wrote to stdout and stderr.
	Out, Err bytes.Buffer

	mu     sync.Mutex
	result batchv1.JobConditionType
}

// New makes a fleet with development, staging and production, a site blog in
// each, and a bucket holding objects (key to size).
func New(tb testing.TB, objects map[string]int64) *Harness {
	tb.Helper()

	h := &Harness{
		Dir:    tb.TempDir(),
		S3:     s3test.New(tb, Bucket, objects),
		Kube:   map[string]*kubetest.Fake{},
		Deps:   backup.Deps{Open: nil, Getenv: nil, Now: nil, Poll: 0, WPCLI: nil},
		Out:    bytes.Buffer{},
		Err:    bytes.Buffer{},
		mu:     sync.Mutex{},
		result: batchv1.JobComplete,
	}

	h.writeFleet(tb)

	phar := testutil.WriteFile(tb, h.Dir, "cache/wp-cli.phar", "<?php // not wp-cli")
	h.Deps = h.deps(phar)

	return h
}

// WriteSite writes the site file of name in env, with extra appended to the
// values every test site has.
func (h *Harness) WriteSite(tb testing.TB, env, name, extra string) {
	tb.Helper()

	testutil.WriteFile(tb, h.Dir, filepath.Join("environments", env, name+".yaml"),
		"site: "+name+"\ndatabase:\n  password: "+Password+"\n"+extra)
}

// FailJobs makes every Job created from now on fail.
func (h *Harness) FailJobs() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.result = batchv1.JobFailed
}

// Seed adds objects to an environment's cluster.
func (h *Harness) Seed(tb testing.TB, env string, objects ...runtime.Object) {
	tb.Helper()

	for _, o := range objects {
		testutil.NoError(tb, h.Kube[env].Clientset.Tracker().Add(o))
	}
}

// Jobs lists the Jobs in an environment's cluster.
func (h *Harness) Jobs(tb testing.TB, env string) []batchv1.Job {
	tb.Helper()

	//nolint:exhaustruct_v5 // list options are all optional
	list, err := h.Kube[env].Clientset.BatchV1().Jobs(Namespace(env)).List(tb.Context(), metav1.ListOptions{})
	testutil.NoError(tb, err)

	return list.Items
}

// Run runs command under a root with the global flags, with stdin, in the
// fleet directory, and checks that nothing it printed holds a secret.
func (h *Harness) Run(tb testing.TB, command *cli.Command, stdin string, args ...string) error {
	tb.Helper()

	h.Out.Reset()
	h.Err.Reset()

	//nolint:exhaustruct_v5 // a test root needs only these
	root := &cli.Command{
		Name:           "wpfleet",
		Flags:          cmdutil.GlobalFlags(),
		Reader:         strings.NewReader(stdin),
		Writer:         &h.Out,
		ErrWriter:      &h.Err,
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		Commands:       []*cli.Command{command},
	}

	err := root.Run(tb.Context(), append([]string{"wpfleet", "-f", h.Dir}, args...))

	for _, secret := range []string{Password, s3test.SecretAccessKey} {
		testutil.NotContains(tb, "stdout", h.Out.String(), secret)
		testutil.NotContains(tb, "stderr", h.Err.String(), secret)
	}

	return err //nolint:wrapcheck // the test reads the command's own error
}

// writeFleet writes wpfleet.yaml, a kubeconfig, and the values of blog in
// every environment, and makes the environments' clusters.
func (h *Harness) writeFleet(tb testing.TB) {
	tb.Helper()

	testutil.WriteFile(tb, h.Dir, "wpfleet.yaml", `environments:
  - name: development
    namespace: wp-development
    kubeconfig: kubeconfig
  - name: staging
    namespace: wp-staging
    kubeconfig: kubeconfig
    promoteFrom: development
  - name: production
    namespace: wp-production
    kubeconfig: kubeconfig
    promoteFrom: staging
backup:
  s3:
    endpoint: `+h.S3.URL+`
    bucket: `+Bucket+`
`)
	testutil.WriteFile(tb, h.Dir, "kubeconfig", `apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: "https://k8s.example.com:6443"}}]
users: [{name: u, user: {token: not-a-real-token}}]
contexts: [{name: c, context: {cluster: c, user: u}}]
current-context: c
`)

	suffixes := map[string]string{Development: "-dev", Staging: "-staging", Production: ""}

	for env, suffix := range suffixes {
		testutil.WriteFile(tb, h.Dir, "environments/"+env+"/_shared.yaml",
			"domainSuffix: example.com\nhostLabelSuffix: \""+suffix+"\"\nbackup:\n  enabled: true\n")
		h.WriteSite(tb, env, "blog", "")
		h.Kube[env] = h.cluster(Namespace(env))
	}
}

// deps are the commands' dependencies on the fakes.
func (h *Harness) deps(phar string) backup.Deps {
	keys := map[string]string{s3.EnvAccessKeyID: s3test.AccessKeyID, s3.EnvSecretAccessKey: s3test.SecretAccessKey}

	return backup.Deps{
		Open: func(f *cmdutil.Fleet, name string) (*cmdutil.Env, error) {
			env, err := f.Open(name)
			if err != nil {
				return nil, err
			}

			env.Kube = h.Kube[name]

			return env, nil
		},
		Getenv: func(k string) string { return keys[k] },
		Now:    Now,
		Poll:   pollInterval,
		WPCLI: func(_ context.Context, _ *cmdutil.Fleet, k kube.Execer) (*wpcli.Runner, error) {
			return wpcli.NewRunner(k, phar), nil
		},
	}
}

// cluster is a kubetest cluster in namespace where a Job finishes as it is
// created and gets one pod.
func (h *Harness) cluster(namespace string) *kubetest.Fake {
	k := kubetest.New(namespace)

	k.Clientset.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create, ok := action.(k8stesting.CreateAction)
		if !ok {
			return false, nil, nil
		}

		job, ok := create.GetObject().(*batchv1.Job)
		if !ok {
			return false, nil, nil
		}

		h.mu.Lock()
		result := h.result
		h.mu.Unlock()

		//nolint:exhaustruct_v5 // the type and status are what WaitJob reads
		job.Status.Conditions = append(job.Status.Conditions, batchv1.JobCondition{
			Type: result, Status: corev1.ConditionTrue, Reason: "Test", Message: "finished at once",
		})

		//nolint:exhaustruct_v5 // a pod of the Job needs only its name and label
		pod := &corev1.Pod{
			Name:              job.Name + "-x1",
			Namespace:         namespace,
			Labels:            map[string]string{"batch.kubernetes.io/job-name": job.Name},
			CreationTimestamp: metav1.Now(),
		}

		return false, nil, k.Clientset.Tracker().Add(pod)
	})

	return k
}

// Env returns the value of a container's environment variable, and its
// secret reference when it has one.
func Env(c corev1.Container, name string) (string, *corev1.SecretKeySelector) {
	for _, e := range c.Env {
		if e.Name != name {
			continue
		}

		if e.ValueFrom != nil {
			return "", e.ValueFrom.SecretKeyRef
		}

		return e.Value, nil
	}

	return "", nil
}
