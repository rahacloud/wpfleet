// Package kubetest is a kube.Interface for tests: the real kube.Client over
// client-go's fake clientset, with an executor that records every command,
// understands the tar -x of kube.CopyFile, test -f and mv -f, refuses a
// command with no stream as the kubelet does, and hands everything else to a
// handler the test sets.
package kubetest

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// pollInterval keeps WaitJob quick in tests.
const pollInterval = 10 * time.Millisecond

// Call is one command the fake executor ran.
type Call struct {
	// Pod and Container say where it ran.
	Pod, Container string
	// Command is the command and its arguments.
	Command []string
	// Stdin is everything the command read.
	Stdin []byte
}

// Handler answers a command the fake does not understand itself. It writes
// the command's output and returns its error; Exit makes a non-zero exit.
type Handler func(call Call, stdout, stderr io.Writer) error

// Fake is a kube.Client on a fake clientset with a recording executor.
type Fake struct {
	*kube.Client

	// Clientset is the fake clientset, for seeding objects and asserting on
	// what the code created.
	Clientset *fake.Clientset

	exec *executor
}

// New returns a fake client in namespace, seeded with objects.
func New(namespace string, objects ...runtime.Object) *Fake {
	clientset := fake.NewClientset(objects...)
	ex := &executor{mu: sync.Mutex{}, calls: nil, files: map[string][]byte{}, handler: nil}

	client := kube.NewForClientset(clientset, namespace, ex)
	client.PollInterval = pollInterval

	return &Fake{Client: client, Clientset: clientset, exec: ex}
}

// Handle sets the handler for commands the fake does not understand itself.
func (f *Fake) Handle(h Handler) {
	f.exec.mu.Lock()
	defer f.exec.mu.Unlock()

	f.exec.handler = h
}

// Calls returns every command run so far, in order.
func (f *Fake) Calls() []Call {
	f.exec.mu.Lock()
	defer f.exec.mu.Unlock()

	return slices.Clone(f.exec.calls)
}

// File returns a file copied into a pod, and whether it exists.
func (f *Fake) File(pod, remotePath string) ([]byte, bool) {
	f.exec.mu.Lock()
	defer f.exec.mu.Unlock()

	data, ok := f.exec.files[pod+":"+remotePath]

	return data, ok
}

// Files lists the pod:path of every file copied in, sorted.
func (f *Fake) Files() []string {
	f.exec.mu.Lock()
	defer f.exec.mu.Unlock()

	return slices.Sorted(maps.Keys(f.exec.files))
}

// ErrExit is the message of every exit error the fake returns.
var ErrExit = errors.New("command terminated with a non-zero exit code")

// ErrBadTar is returned for a tar -x the fake cannot follow.
var ErrBadTar = errors.New("fake exec: tar -x without -C, or a path that leaves -C")

// Exit is an error that kube.ExitCode reads as a command's exit code.
func Exit(code int) error {
	return utilexec.CodeExitError{Err: fmt.Errorf("%w: %d", ErrExit, code), Code: code}
}

// executor is the fake kube.Executor.
type executor struct {
	mu      sync.Mutex
	calls   []Call
	files   map[string][]byte
	handler Handler
}

// Exec records the call and runs it.
func (e *executor) Exec(_ context.Context, req kube.ExecRequest) error {
	err := req.Validate()
	if err != nil {
		return err
	}

	stdin, err := readAll(req.Stdin)
	if err != nil {
		return err
	}

	call := Call{Pod: req.Pod, Container: req.Container, Command: slices.Clone(req.Command), Stdin: stdin}

	stdout, stderr := orDiscard(req.Stdout), orDiscard(req.Stderr)

	e.mu.Lock()
	e.calls = append(e.calls, call)
	handler := e.handler
	e.mu.Unlock()

	switch {
	case isTestF(call.Command):
		return e.testFile(call.Pod, call.Command[2])
	case isMove(call.Command):
		return e.move(call.Pod, call.Command[2], call.Command[3])
	case len(call.Command) > 0 && call.Command[0] == "tar" && slices.Contains(call.Command, "-x"):
		return e.untar(call)
	case handler != nil:
		return handler(call, stdout, stderr)
	default:
		return nil
	}
}

// isTestF reports whether cmd is test -f <path>.
func isTestF(cmd []string) bool {
	return len(cmd) == 3 && cmd[0] == "test" && cmd[1] == "-f"
}

// testFile answers test -f: exit 1 when the file was never copied in.
func (e *executor) testFile(pod, file string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.files[pod+":"+file]; !ok {
		return Exit(1)
	}

	return nil
}

// isMove reports whether cmd is mv -f <from> <to>.
func isMove(cmd []string) bool {
	return len(cmd) == 4 && cmd[0] == "mv" && cmd[1] == "-f"
}

// move answers mv -f: exit 1 when the file was never copied in.
func (e *executor) move(pod, from, to string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, ok := e.files[pod+":"+from]
	if !ok {
		return Exit(1)
	}

	delete(e.files, pod+":"+from)
	e.files[pod+":"+to] = data

	return nil
}

// untar stores the files of a tar -x ... -C dir call.
func (e *executor) untar(call Call) error {
	i := slices.Index(call.Command, "-C")
	if i < 0 || i+1 >= len(call.Command) {
		return ErrBadTar
	}

	dir := call.Command[i+1]
	tr := tar.NewReader(bytes.NewReader(call.Stdin))

	e.mu.Lock()
	defer e.mu.Unlock()

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("fake exec: reading tar: %w", err)
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return fmt.Errorf("fake exec: reading tar: %w", err)
		}

		name := path.Join(dir, hdr.Name) //nolint:gosec // checked to stay inside dir just below
		if !strings.HasPrefix(name, path.Clean(dir)+"/") {
			return fmt.Errorf("%w: %s", ErrBadTar, hdr.Name)
		}

		e.files[call.Pod+":"+name] = data
	}
}

// RunningPod returns a pod of site that kube.Serving accepts.
func RunningPod(namespace, site, name string) *corev1.Pod {
	//nolint:exhaustruct_v5 // a test pod needs only what the selectors read
	return &corev1.Pod{
		Name:              name,
		Namespace:         namespace,
		Labels:            map[string]string{kube.LabelInstance: site},
		CreationTimestamp: metav1.Now(),
		Spec:              corev1.PodSpec{Containers: []corev1.Container{{Name: kube.WordPressContainer}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

// SetJobCondition marks a Job in the fake clientset complete or failed.
func (f *Fake) SetJobCondition(ctx context.Context, name string, condition batchv1.JobConditionType) error {
	//nolint:exhaustruct_v5 // get options are all optional
	job, err := f.Clientset.BatchV1().Jobs(f.Namespace()).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("fake: reading Job %s: %w", name, err)
	}

	//nolint:exhaustruct_v5 // the condition's type and status are what WaitJob reads
	cond := batchv1.JobCondition{Type: condition, Status: corev1.ConditionTrue, Reason: "Test", Message: string(condition)}
	job.Status.Conditions = append(job.Status.Conditions, cond)

	//nolint:exhaustruct_v5 // update options are all optional
	_, err = f.Clientset.BatchV1().Jobs(f.Namespace()).UpdateStatus(ctx, job, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("fake: updating Job %s: %w", name, err)
	}

	return nil
}

// readAll reads a command's stdin; nil reads nothing.
func readAll(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("fake exec: reading stdin: %w", err)
	}

	return data, nil
}

// orDiscard turns a nil writer into io.Discard.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}

	return w
}
