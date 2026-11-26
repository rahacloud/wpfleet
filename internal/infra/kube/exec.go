package kube

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
	"k8s.io/streaming/pkg/httpstream"
)

// ErrRelativePath is returned for a remote path that is not absolute.
var ErrRelativePath = errors.New("remote path must be absolute")

// ErrNoStreams is returned for an exec request with no stream attached, which
// the kubelet refuses ("you must specify at least 1 of stdin, stdout, stderr").
var ErrNoStreams = errors.New("exec needs at least one of stdin, stdout and stderr")

// ExecRequest is one command to run in a container.
type ExecRequest struct {
	// Namespace, Pod and Container say where the command runs. An empty
	// Container lets the API server pick the pod's only container.
	Namespace, Pod, Container string
	// Command is the command and its arguments; no shell is involved.
	Command []string
	// Stdin, Stdout and Stderr are attached when not nil.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Validate refuses a request the kubelet would refuse: one with no stream.
func (r ExecRequest) Validate() error {
	if r.Stdin == nil && r.Stdout == nil && r.Stderr == nil {
		return fmt.Errorf("%w: %v in %s", ErrNoStreams, r.Command, r.Pod)
	}

	return nil
}

// Executor runs a command in a container. The remote one streams through the
// API server; kubetest has a fake.
type Executor interface {
	Exec(ctx context.Context, req ExecRequest) error
}

// RemoteExecutor runs commands through the API server's exec subresource,
// over WebSocket and, when the server or a proxy refuses the upgrade, over
// SPDY, the way kubectl exec does.
type RemoteExecutor struct {
	config *rest.Config
	client rest.Interface
}

// NewRemoteExecutor makes an executor from a REST config and the core/v1 REST
// client of the same cluster.
func NewRemoteExecutor(config *rest.Config, client rest.Interface) *RemoteExecutor {
	return &RemoteExecutor{config: config, client: client}
}

// Exec runs the request and waits for the command to exit.
func (r *RemoteExecutor) Exec(ctx context.Context, req ExecRequest) error {
	err := req.Validate()
	if err != nil {
		return err
	}

	opts := &corev1.PodExecOptions{ //nolint:exhaustruct_v5 // TypeMeta is filled in by the codec
		Container: req.Container,
		Command:   req.Command,
		Stdin:     req.Stdin != nil,
		Stdout:    req.Stdout != nil,
		Stderr:    req.Stderr != nil,
		TTY:       false,
	}

	u := r.client.Post().
		Namespace(req.Namespace).
		Resource("pods").
		Name(req.Pod).
		SubResource("exec").
		VersionedParams(opts, scheme.ParameterCodec).
		URL()

	websocket, err := remotecommand.NewWebSocketExecutor(r.config, http.MethodGet, u.String())
	if err != nil {
		return fmt.Errorf("exec over WebSocket: %w", err)
	}

	spdy, err := remotecommand.NewSPDYExecutor(r.config, http.MethodPost, u)
	if err != nil {
		return fmt.Errorf("exec over SPDY: %w", err)
	}

	executor, err := remotecommand.NewFallbackExecutor(websocket, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	//nolint:exhaustruct_v5 // no terminal: Tty and TerminalSizeQueue stay unset
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  req.Stdin,
		Stdout: req.Stdout,
		Stderr: req.Stderr,
	})
	if err != nil {
		return fmt.Errorf("exec in %s/%s: %w", req.Pod, req.Container, err)
	}

	return nil
}

// ExitCode returns the exit code of a command that Exec ran and that exited
// non-zero; ok is false for any other error.
func ExitCode(err error) (int, bool) {
	exit, ok := errors.AsType[utilexec.ExitError](err)
	if !ok {
		return 0, false
	}

	return exit.ExitStatus(), true
}

// Exec runs cmd in a container of pod.
func (c *Client) Exec(
	ctx context.Context, pod, container string, cmd []string, stdin io.Reader, stdout, stderr io.Writer,
) error {
	return c.executor.Exec(ctx, ExecRequest{
		Namespace: c.namespace,
		Pod:       pod,
		Container: container,
		Command:   cmd,
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}

// CopyFile copies a local file into the WordPress container of pod at
// remotePath, as a tar stream into tar -x, the way kubectl cp does. The
// directory of remotePath must exist.
func (c *Client) CopyFile(ctx context.Context, pod, localPath, remotePath string) error {
	if !path.IsAbs(remotePath) {
		return fmt.Errorf("%w: %s", ErrRelativePath, remotePath)
	}

	f, err := os.Open(localPath) //nolint:gosec // the caller names the file to copy
	if err != nil {
		return fmt.Errorf("copying into %s: %w", pod, err)
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("copying into %s: %w", pod, err)
	}

	pr, pw := io.Pipe()

	go func() {
		pw.CloseWithError(writeTar(pw, f, info, path.Base(remotePath)))
	}()

	cmd := []string{"tar", "-x", "-m", "-f", "-", "-C", path.Dir(remotePath)}

	err = c.Exec(ctx, pod, WordPressContainer, cmd, pr, io.Discard, io.Discard)

	// Unblock the writer if tar stopped reading early.
	_ = pr.CloseWithError(io.ErrClosedPipe)

	if err != nil {
		return fmt.Errorf("copying %s into %s:%s: %w", localPath, pod, remotePath, err)
	}

	return nil
}

// writeTar writes one regular file named name to w as a tar archive.
func writeTar(w io.Writer, r io.Reader, info os.FileInfo, name string) error {
	tw := tar.NewWriter(w)

	//nolint:exhaustruct_v5 // a plain file needs only these
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     name,
		Mode:     int64(info.Mode().Perm()),
		Size:     info.Size(),
		ModTime:  info.ModTime(),
	}

	err := tw.WriteHeader(hdr)
	if err != nil {
		return fmt.Errorf("writing tar header: %w", err)
	}

	_, err = io.Copy(tw, r)
	if err != nil {
		return fmt.Errorf("writing tar: %w", err)
	}

	err = tw.Close()
	if err != nil {
		return fmt.Errorf("writing tar: %w", err)
	}

	return nil
}
