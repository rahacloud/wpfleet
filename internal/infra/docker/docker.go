// Package docker builds a site's image with the docker CLI and pushes it. The
// build context goes to docker build on stdin as a tar stream that wpfleet
// writes itself, because BuildKit's own context transfer refuses file names
// that are not valid UTF-8 and old WordPress trees often have some.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"time"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
)

// ErrInvalidTag is returned for a tag docker would refuse.
var ErrInvalidTag = errors.New(
	"image tag must be letters, digits, '_', '.' and '-', at most 128, not starting with '.' or '-'")

// ErrInvalidSpec is returned for a build specification that is incomplete.
var ErrInvalidSpec = errors.New("invalid build")

// errStoppedReading ends the context writer when docker build exits before
// reading the whole stream.
var errStoppedReading = errors.New("docker build stopped reading the context")

// tagPattern is docker's rule for a tag.
var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// TagLayout is the layout of the default tag, a UTC time stamp.
const TagLayout = "20060102-150405"

// ValidateTag reports whether tag is a tag docker accepts.
func ValidateTag(tag string) error {
	if !tagPattern.MatchString(tag) {
		return fmt.Errorf("%q: %w", tag, ErrInvalidTag)
	}

	return nil
}

// DefaultTag is the tag a build gets when none is given: the time in UTC as
// YYYYMMDD-HHMMSS.
func DefaultTag(now time.Time) string {
	return now.UTC().Format(TagLayout)
}

// Runner runs the docker CLI with args, feeding it stdin. It is an interface
// so that tests run without docker.
type Runner interface {
	// Run runs docker with args and returns an error when it fails.
	Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error
}

// CLI runs the docker binary found on PATH, or the one Binary names.
type CLI struct {
	// Binary is the docker executable; empty means "docker".
	Binary string
}

// Run runs the docker binary. Cancelling ctx kills it.
func (c CLI) Run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	bin := c.Binary
	if bin == "" {
		bin = "docker"
	}

	slog.DebugContext(ctx, "running docker", "args", args)

	//nolint:gosec // running docker with wpfleet's own arguments is this package's job
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("%s %s: %w", bin, first(args), err)
	}

	return nil
}

// first is the docker subcommand of args, for error messages.
func first(args []string) string {
	if len(args) == 0 {
		return ""
	}

	return args[0]
}

// Spec is what to build: a site's code tree on a base image, tagged as
// Image.
type Spec struct {
	// Site is the site's name.
	Site string
	// Source is the site's code tree, the directory that becomes
	// /var/www/html.
	Source string
	// Base is the image the site image is built on.
	Base string
	// Image is the reference to tag and push.
	Image fleet.ImageRef
	// Created is the build time, recorded in the image's labels.
	Created time.Time
}

// validate checks that the spec is complete and that nothing in it can break
// out of the generated Dockerfile.
func (s Spec) validate() error {
	switch {
	case s.Site == "":
		return fmt.Errorf("%w: no site", ErrInvalidSpec)
	case s.Source == "":
		return fmt.Errorf("%w: no source directory", ErrInvalidSpec)
	case s.Image.Repository == "" || s.Image.Tag == "":
		return fmt.Errorf("%w: image %q needs a repository and a tag", ErrInvalidSpec, s.Image.String())
	}

	err := fleet.ValidateSiteName(s.Site)
	if err != nil {
		return err
	}

	err = ValidateTag(s.Image.Tag)
	if err != nil {
		return err
	}

	_, err = fleet.ParseImageRef(s.Base)
	if err != nil {
		return fmt.Errorf("build.base: %w", err)
	}

	return nil
}

// Client builds and pushes images through a Runner. Docker's own output, the
// build progress included, goes to Log.
type Client struct {
	runner Runner
	log    io.Writer
}

// New returns a client that runs docker through runner and sends its output
// to log.
func New(runner Runner, log io.Writer) *Client {
	return &Client{runner: runner, log: log}
}

// Build runs docker build with the context streamed on stdin: the generated
// Dockerfile and the source tree without the excluded paths.
func (c *Client) Build(ctx context.Context, spec Spec) error {
	err := spec.validate()
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() {
		err := WriteContext(pw, spec)
		_ = pw.CloseWithError(err) // always nil
		done <- err
	}()

	runErr := c.runner.Run(ctx, pr, c.log, c.log, "build", "--tag", spec.Image.String(), "-")
	// Unblock the writer when docker stopped reading before the end.
	_ = pr.CloseWithError(errStoppedReading) // always nil

	writeErr := <-done

	if runErr != nil {
		return runErr
	}

	if writeErr != nil {
		return fmt.Errorf("writing the build context: %w", writeErr)
	}

	return nil
}

// Push runs docker push for ref.
func (c *Client) Push(ctx context.Context, ref fleet.ImageRef) error {
	return c.runner.Run(ctx, nil, c.log, c.log, "push", ref.String())
}
