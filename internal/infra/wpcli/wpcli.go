// Package wpcli fetches wp-cli.phar once into the user's cache, copies it into
// a site's pod (pods often have no egress) and runs it there.
package wpcli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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

// remoteDir is where the phar lives in a pod.
const remoteDir = "/tmp"

// DocumentRoot is WordPress's directory in the official image.
const DocumentRoot = "/var/www/html"

// cacheSubdir is wpfleet's directory in the user's cache.
const cacheSubdir = "wpfleet"

// cacheDirMode is the mode of a cache directory wpfleet creates.
const cacheDirMode = 0o750

// keyLen is how many hex digits of a hash name a cached or copied phar.
const keyLen = 32

// partSuffixBytes is how many random bytes name a copy in progress.
const partSuffixBytes = 8

// ErrChecksum is returned when the phar's SHA-512 is not the configured one.
var ErrChecksum = errors.New("wp-cli.phar checksum mismatch")

// ErrNoChecksum is returned for a source without a SHA-512 that does not
// allow an unverified phar.
var ErrNoChecksum = errors.New(
	"wp-cli.phar has no sha512: set wpcli.sha512, or wpcli.allowUnverified to run it unchecked")

// Source says where the phar comes from and how it is checked.
type Source struct {
	// URL is the phar's download URL.
	URL string
	// SHA512 is the phar's hex SHA-512.
	SHA512 string
	// AllowUnverified lets a source without SHA512 be used unchecked.
	AllowUnverified bool
}

// Ensure returns the path of the phar from src in the user's cache,
// downloading it the first time.
func Ensure(ctx context.Context, src Source) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache directory: %w", err)
	}

	return EnsureIn(ctx, http.DefaultClient, filepath.Join(cache, cacheSubdir), src)
}

// EnsureIn is Ensure with the HTTP client and the cache directory given. The
// phar's SHA-512 must match src.SHA512; a source without one is refused with
// ErrNoChecksum unless it allows an unverified phar. The cache entry is named
// after the sum, or after a hash of the URL when there is none, so a changed
// URL or sum never reuses another file.
func EnsureIn(ctx context.Context, client *http.Client, dir string, src Source) (string, error) {
	sum := strings.ToLower(strings.TrimSpace(src.SHA512))
	if sum == "" && !src.AllowUnverified {
		return "", ErrNoChecksum
	}

	_, err := url.Parse(src.URL)
	if err != nil {
		return "", fmt.Errorf("wp-cli URL: %w", err)
	}

	local := filepath.Join(dir, cacheName(src.URL, sum))

	if cached(local, sum) {
		return local, nil
	}

	err = os.MkdirAll(dir, cacheDirMode)
	if err != nil {
		return "", fmt.Errorf("creating the cache directory: %w", err)
	}

	err = download(ctx, client, src.URL, sum, local)
	if err != nil {
		return "", err
	}

	return local, nil
}

// cacheName names the cache entry of a phar: by its sum when there is one,
// else by a hash of its URL.
func cacheName(rawURL, sum string) string {
	if sum != "" {
		return "wp-cli-" + sum[:min(keyLen, len(sum))] + ".phar"
	}

	h := sha256.Sum256([]byte(rawURL))

	return "wp-cli-url-" + hex.EncodeToString(h[:])[:keyLen] + ".phar"
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
// interrupted download or one that does not match sum never leaves a phar in
// the cache.
func download(ctx context.Context, client *http.Client, rawURL, sum, local string) error {
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

	if sum != "" {
		err = verify(tmp.Name(), sum)
		if err != nil {
			return err
		}
	}

	err = os.Rename(tmp.Name(), local)
	if err != nil {
		return fmt.Errorf("downloading wp-cli: %w", err)
	}

	return nil
}

// verify checks a file's SHA-512 against the hex sum.
func verify(file, sum string) error {
	got, err := fileSum(file)
	if err != nil {
		return err
	}

	if got != sum {
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

	remote func() (string, error)
	mu     sync.Mutex
	copied map[string]bool
}

// NewRunner makes a runner that copies phar into pods through k.
func NewRunner(k kube.Execer, phar string) *Runner {
	return &Runner{
		Kube: k,
		Phar: phar,
		remote: sync.OnceValues(func() (string, error) {
			sum, err := fileSum(phar)
			if err != nil {
				return "", err
			}

			return RemotePath(sum), nil
		}),
		mu:     sync.Mutex{},
		copied: map[string]bool{},
	}
}

// RemotePath is where a phar with the hex SHA-512 sum lives in a pod. Naming
// it by its content means a pod never runs another phar than the local one.
func RemotePath(sum string) string {
	return path.Join(remoteDir, "wp-cli-"+sum[:min(keyLen, len(sum))]+".phar")
}

// Command is the command that runs the phar at remote with args in a pod.
func Command(remote string, args ...string) []string {
	return append([]string{"php", remote, "--allow-root", "--path=" + DocumentRoot}, args...)
}

// Run runs wp-cli with args in the WordPress container of pod, copying the
// phar in first when the pod does not have it yet, and streams its output.
func (r *Runner) Run(ctx context.Context, pod string, stdout, stderr io.Writer, args ...string) error {
	remote, err := r.ensureCopied(ctx, pod)
	if err != nil {
		return err
	}

	err = r.Kube.Exec(ctx, pod, kube.WordPressContainer, Command(remote, args...), nil, stdout, stderr)
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
// pod already has it, and returns its path in the pod. The copy goes to a
// temporary name and is renamed into place, so an interrupted copy is never
// taken for the phar.
func (r *Runner) ensureCopied(ctx context.Context, pod string) (string, error) {
	remote, err := r.remote()
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.copied[pod] {
		return remote, nil
	}

	// The exec API refuses a command with no stream attached.
	var stderr bytes.Buffer

	err = r.Kube.Exec(ctx, pod, kube.WordPressContainer, []string{"test", "-f", remote}, nil, io.Discard, &stderr)

	switch _, exited := kube.ExitCode(err); {
	case err == nil:
	case exited:
		err := r.copyIn(ctx, pod, remote)
		if err != nil {
			return "", err
		}
	default:
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}

		return "", fmt.Errorf("looking for wp-cli in %s: %w", pod, err)
	}

	if r.copied == nil {
		r.copied = map[string]bool{}
	}

	r.copied[pod] = true

	return remote, nil
}

// copyIn copies the phar into pod at a temporary name and renames it to
// remote.
func (r *Runner) copyIn(ctx context.Context, pod, remote string) error {
	suffix := make([]byte, partSuffixBytes)
	_, _ = rand.Read(suffix) // crypto/rand.Read never fails

	tmp := remote + "." + hex.EncodeToString(suffix) + ".part"

	err := r.Kube.CopyFile(ctx, pod, r.Phar, tmp)
	if err != nil {
		return err
	}

	var stderr bytes.Buffer

	err = r.Kube.Exec(ctx, pod, kube.WordPressContainer, []string{"mv", "-f", tmp, remote}, nil, io.Discard, &stderr)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}

		return fmt.Errorf("moving wp-cli into place in %s: %w", pod, err)
	}

	return nil
}

// fileSum is the hex SHA-512 of a file.
func fileSum(file string) (string, error) {
	f, err := os.Open(file) //nolint:gosec // the file is the phar the caller names
	if err != nil {
		return "", fmt.Errorf("hashing wp-cli: %w", err)
	}

	defer func() { _ = f.Close() }()

	h := sha512.New()

	_, err = io.Copy(h, f)
	if err != nil {
		return "", fmt.Errorf("hashing wp-cli: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}

	return args[0]
}
