// Package kube talks to the cluster of one environment with client-go: the
// pods of a site, exec and file copies into a pod, Jobs started from a
// CronJob, and pod logs. Commands depend on the small interfaces here, so
// their tests can use kubetest instead of a cluster.
package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
)

// WordPressContainer is the name of the chart's WordPress container.
const WordPressContainer = "wordpress"

// Labels the chart puts on a site's objects.
const (
	LabelInstance   = "app.kubernetes.io/instance"
	LabelComponent  = "app.kubernetes.io/component"
	ComponentBackup = "backup"
)

// defaultPollInterval is how often WaitJob looks at a Job.
const defaultPollInterval = 2 * time.Second

// ErrNoNamespace is returned for a target without a namespace.
var ErrNoNamespace = errors.New("namespace is required")

// Target is where an environment's releases live: a kubeconfig, a context in
// it and a namespace.
type Target struct {
	// Kubeconfig is a kubeconfig file; empty follows the usual KUBECONFIG
	// rules.
	Kubeconfig string
	// Context is a context of the kubeconfig; empty uses the current one.
	Context string
	// Namespace is the namespace every release of the environment lives in.
	Namespace string
}

// TargetFor returns the target of an environment.
func TargetFor(env fleet.Environment) Target {
	return Target{Kubeconfig: env.Kubeconfig, Context: env.Context, Namespace: env.Namespace}
}

// ClientConfig loads the kubeconfig the way kubectl does, with the target's
// file, context and namespace on top.
//
//nolint:ireturn // clientcmd hands out its config only as this interface
func (t Target) ClientConfig() clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if t.Kubeconfig != "" {
		rules.ExplicitPath = t.Kubeconfig
	}

	//nolint:exhaustruct_v5 // client-go's overrides are optional by design
	overrides := &clientcmd.ConfigOverrides{CurrentContext: t.Context}
	overrides.Context.Namespace = t.Namespace

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
}

// RESTClientGetter is the target in the form the Helm SDK takes.
//
//nolint:ireturn // the Helm SDK takes exactly this interface
func (t Target) RESTClientGetter() genericclioptions.RESTClientGetter {
	flags := genericclioptions.NewConfigFlags(false)
	flags.KubeConfig = &t.Kubeconfig
	flags.Context = &t.Context
	flags.Namespace = &t.Namespace

	return flags
}

// Execer runs commands in a pod and copies files into it.
type Execer interface {
	// Exec runs cmd in a container of pod and waits for it. A command that
	// exits non-zero returns an error that ExitCode understands.
	Exec(ctx context.Context, pod, container string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
	// CopyFile copies a local file into the WordPress container of pod.
	CopyFile(ctx context.Context, pod, localPath, remotePath string) error
}

// Pods finds a site's pods.
type Pods interface {
	// SitePods lists the pods of a site, backup pods left out.
	SitePods(ctx context.Context, site string) ([]corev1.Pod, error)
	// RunningPod returns a running, ready pod of a site.
	RunningPod(ctx context.Context, site string) (corev1.Pod, error)
}

// Jobs starts Jobs from CronJobs and follows them.
type Jobs interface {
	// JobFromCronJob creates a Job named jobName from the CronJob's template,
	// as kubectl create job --from=cronjob/<name> does.
	JobFromCronJob(ctx context.Context, cronJob, jobName string) (batchv1.Job, error)
	// WaitJob waits until the Job completes, and fails when it fails.
	WaitJob(ctx context.Context, name string) error
	// JobPods lists the pods of a Job, oldest first.
	JobPods(ctx context.Context, job string) ([]corev1.Pod, error)
	// StreamLogs follows a container's log into w until it ends.
	StreamLogs(ctx context.Context, pod, container string, w io.Writer) error
}

// Interface is everything the commands need from a cluster.
type Interface interface {
	Execer
	Pods
	Jobs
	// Namespace is the namespace the client works in.
	Namespace() string
}

// Client is Interface on client-go, bound to one namespace.
type Client struct {
	clientset kubernetes.Interface
	namespace string
	executor  Executor
	// PollInterval is how often WaitJob looks at a Job.
	PollInterval time.Duration
}

var _ Interface = (*Client)(nil)

// New connects to the target's cluster. It does not contact the cluster yet.
func New(t Target) (*Client, error) {
	if t.Namespace == "" {
		return nil, ErrNoNamespace
	}

	config, err := t.ClientConfig().ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}

	return NewForConfig(config, t.Namespace)
}

// NewForConfig makes a client from a REST config.
func NewForConfig(config *rest.Config, namespace string) (*Client, error) {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}

	return NewForClientset(clientset, namespace, NewRemoteExecutor(config, clientset.CoreV1().RESTClient())), nil
}

// NewForClientset makes a client from a clientset and an executor; kubetest
// uses it with the fake clientset.
func NewForClientset(clientset kubernetes.Interface, namespace string, executor Executor) *Client {
	return &Client{clientset: clientset, namespace: namespace, executor: executor, PollInterval: defaultPollInterval}
}

// Namespace is the namespace the client works in.
func (c *Client) Namespace() string {
	return c.namespace
}

// Clientset is the underlying clientset, for what the interfaces do not cover.
//
//nolint:ireturn // the fake clientset in tests must fit in the same place
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}
