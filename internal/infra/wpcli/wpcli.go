// Package wpcli fetches wp-cli.phar once into the user's cache, copies it into
// a site's pod (pods often have no egress) and runs it there.
package wpcli

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rahacloud/wpfleet/internal/infra/kube"
)

// RemotePath is where the phar lives in a pod.
const RemotePath = "/tmp/wp-cli.phar"

// DocumentRoot is WordPress's directory in the official image.
const DocumentRoot = "/var/www/html"

// cacheSubdir is wpfleet's directory in the user's cache.
const cacheSubdir = "wpfleet"

// cacheDirMode is the mode of a cache directory wpfleet creates.
const cacheDirMode = 0o750

// ErrChecksum is returned when the phar's SHA-512 is not the configured one.
var ErrChecksum = errors.New("wp-cli.phar checksum mismatch")

// Ensure returns the path of the phar from rawURL in the user's cache,
// downloading it the first time. When sum is set, the file's SHA-512 must
// match it, and a cached file that does not is downloaded again.
func Ensure(ctx context.Context, rawURL, sum string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory: %w", err)
	}

	return EnsureIn(ctx, http.DefaultClient, filepath.Join(cache, cacheSubdir), rawURL, sum)
}

// EnsureIn is Ensure with the HTTP client and the cache directory given.
func EnsureIn(ctx context.Context, client *http.Client, dir, rawURL, sum string) (string, error) {
	local, err := cachePath(dir, rawURL)
	if err != nil {
		return "", err
	}

	sum = strings.ToLower(strings.TrimSpace(sum))

	if cached(local, sum) {
		return local, nil
	}

	err = os.MkdirAll(dir, cacheDirMode)
	if err != nil {
		return "", fmt.Errorf("creating the cache directory: %w", err)
	}

	err = download(ctx, client, rawURL, local)
	if err != nil {
		return "", err
	}

	if sum == "" {
		return local, nil
	}

	err = verify(local, sum)
	if err != nil {
		_ = os.Remove(local)

		return "", err
	}

	return local, nil
}

// cachePath is where the phar from rawURL is cached in dir, named as in the
// URL.
func cachePath(dir, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("wp-cli URL: %w", err)
	}

	name := path.Base(u.Path)
	if name == "." || name == "/" {
		name = "wp-cli.phar"
	}

	return filepath.Join(dir, name), nil
}

// cached reports whether local exists and, when sum is set, matches it.
func cached(local, sum string) bool {
	_, err := os.Stat(local)
	if err != nil {
		return false
	}

	return sum == "" || verify(local, sum) == nil
}

// download fetches rawURL into local through a temporary file, so that an
// interrupted download never leaves a partial phar in the cache.
func download(ctx context.Context, client *http.Client, rawURL, local string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading wp-cli from %s: %s", rawURL, resp.Status) //nolint:err113 // the status is the message
	}

	tmp, err := os.CreateTemp(filepath.Dir(local), ".wp-cli-*")
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, cerr := io.Copy(tmp, resp.Body)

	err = errors.Join(cerr, tmp.Close())
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	err = os.Rename(tmp.Name(), local)
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	return nil
}

// verify checks a file's SHA-512 against the hex sum.
func verify(file, sum string) error {
	f, err := os.Open(file) //nolint:gosec // the file is wpfleet's own cache entry
	if err != nil {
		return fmt.Errorf("verifying wp-cli: %w", err)
	}

	defer func() { _ = f.Close() }()

	h := sha512.New()
	_, err = io.Copy(h, f)
	if err != nil {
		return fmt.Errorf("verifying wp-cli: %w", err)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%w: got %s, want %s", ErrChecksum, got, sum)
	}

	return nil
}

// Runner runs wp-cli in sites' pods.
type Runner struct {
	// Kube runs the commands.
	Kube kube.Execer
	// Phar is the local wp-cli.phar, from Ensure.
	Phar string

	mu     sync.Mutex
	copied map[string]bool
}

// NewRunner makes a runner that copies phar into pods through k.
func NewRunner(k kube.Execer, phar string) *Runner {
	return &Runner{Kube: k, Phar: phar, mu: sync.Mutex{}, copied: map[string]bool{}}
}

// Command is the command that runs wp-cli with args in a pod.
func Command(args ...string) []string {
	return append([]string{"php", RemotePath, "--allow-root", "--path=" + DocumentRoot}, args...)
}

// Run runs wp-cli with args in the WordPress container of pod, copying the
// phar in first when the pod does not have it yet, and streams its output.
func (r *Runner) Run(ctx context.Context, pod string, stdout, stderr io.Writer, args ...string) error {
	err := r.ensureCopied(ctx, pod)
	if err != nil {
		return err
	}

	err = r.Kube.Exec(ctx, pod, kube.WordPressContainer, Command(args...), nil, stdout, stderr)
	if err != nil {
		return fmt.Errorf("wp %s: %w", firstArg(args), err)
	}

	return nil
}

// Output runs wp-cli like Run and returns its standard output, trimmed. The
// standard error is part of the error when the command fails.
func (r *Runner) Output(ctx context.Context, pod string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	err := r.Run(ctx, pod, &stdout, &stderr, args...)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}

		return "", err
	}

	return strings.TrimSpace(stdout.String()), nil
}

// ensureCopied copies the phar into pod unless this runner already did or the
// pod already has it.
func (r *Runner) ensureCopied(ctx context.Context, pod string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.copied[pod] {
		return nil
	}

	err := r.Kube.Exec(ctx, pod, kube.WordPressContainer, []string{"test", "-f", RemotePath}, nil, nil, nil)

	switch _, exited := kube.ExitCode(err); {
	case err == nil:
	case exited:
		err := r.Kube.CopyFile(ctx, pod, r.Phar, RemotePath)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("looking for wp-cli in %s: %w", pod, err)
	}

	if r.copied == nil {
		r.copied = map[string]bool{}
	}

	r.copied[pod] = true

	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}

	return args[0]
}
