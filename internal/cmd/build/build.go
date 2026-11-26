// Package build is the build command: it builds a site's image from its code
// tree on the fleet's base image, pushes it, and with --set writes it into the
// site's file.
package build

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/docker"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Flag names of the build command.
const (
	flagSource = "source"
	flagTag    = "tag"
	flagSet    = "set"
)

// ErrOneSite is returned when build gets no site or more than one.
var ErrOneSite = errors.New("build takes exactly one site")

// ErrNoRegistry is returned when wpfleet.yaml names no registry to push to.
var ErrNoRegistry = errors.New("wpfleet.yaml has no registry to push the image to")

// ErrNotDirectory is returned when --source is not a directory.
var ErrNotDirectory = errors.New("--source is not a directory")

// Result is what build prints: the image it pushed, and the site file it
// wrote the image into with --set.
type Result struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Image is the pushed image reference.
	Image string `json:"image"`
	// File is the site file --set changed; empty without --set.
	File string `json:"file,omitempty"`
}

// Command is wpfleet build, running the docker CLI.
func Command() *cli.Command {
	return newCommand(docker.CLI{Binary: ""}, time.Now)
}

// newCommand builds the command on a docker runner and a clock, which tests
// replace.
func newCommand(runner docker.Runner, now func() time.Time) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "build",
		Usage:     "build a site's image from its code tree and push it",
		ArgsUsage: "<site>",
		Description: "Builds <registry>/<site>:<tag> on build.base from the code tree in --source and pushes it. " +
			"wp-config.php, wp-content/uploads, wp-content/cache, wp-content/upgrade and every error_log " +
			"stay out of the image. With --set the new image is written into the site's file in --env.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     flagSource,
				Usage:    "the site's code tree, which becomes /var/www/html",
				Required: true,
			},
			&cli.StringFlag{
				Name:  flagTag,
				Usage: "the image tag (default: the UTC time as YYYYMMDD-HHMMSS)",
			},
			&cli.BoolFlag{
				Name:  flagSet,
				Usage: "write the new image into the site's file in --env",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return run(ctx, cmd, runner, now)
		},
	}
}

// run is the build command's action.
func run(ctx context.Context, cmd *cli.Command, runner docker.Runner, now func() time.Time) error {
	if cmd.NArg() != 1 {
		return ErrOneSite
	}

	name := cmd.Args().First()

	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	if f.Fleet.Registry == "" {
		return ErrNoRegistry
	}

	created := now()

	tag := cmd.String(flagTag)
	if tag == "" {
		tag = docker.DefaultTag(created)
	}

	file, err := siteFile(cmd, f, name)
	if err != nil {
		return err
	}

	source := cmd.String(flagSource)

	err = checkDir(source)
	if err != nil {
		return err
	}

	spec := docker.Spec{
		Site:    name,
		Source:  source,
		Base:    f.Config.Build.Base,
		Image:   f.Fleet.Image(name, tag),
		Created: created,
	}

	err = publish(ctx, docker.New(runner, cmd.Root().ErrWriter), spec)
	if err != nil {
		return err
	}

	if file != "" {
		err = values.SetImage(file, spec.Image.Repository, spec.Image.Tag)
		if err != nil {
			return fmt.Errorf("the image %s is pushed, but setting it failed: %w", spec.Image.String(), err)
		}
	}

	return cmdutil.Print(cmd, []Result{{Site: name, Image: spec.Image.String(), File: file}},
		cmdutil.Column[Result]{Header: "site", Value: func(r Result) string { return r.Site }},
		cmdutil.Column[Result]{Header: "image", Value: func(r Result) string { return r.Image }},
		cmdutil.Column[Result]{Header: "set", Value: func(r Result) string { return r.File }},
	)
}

// publish builds the image of spec and pushes it.
func publish(ctx context.Context, client *docker.Client, spec docker.Spec) error {
	slog.DebugContext(ctx, "building", "site", spec.Site, "image", spec.Image.String(), "source", spec.Source)

	err := client.Build(ctx, spec)
	if err != nil {
		return err
	}

	return client.Push(ctx, spec.Image)
}

// siteFile checks the site's name and, with --set, returns the site's file in
// --env; it fails before the build when that file does not exist.
func siteFile(cmd *cli.Command, f *cmdutil.Fleet, name string) (string, error) {
	err := fleet.ValidateSiteName(name)
	if err != nil {
		return "", err
	}

	if !cmd.Bool(flagSet) {
		return "", nil
	}

	envName := cmd.String(cmdutil.FlagEnv)
	if envName == "" {
		return "", fmt.Errorf("--set: %w", cmdutil.ErrNoEnv)
	}

	env, err := f.Fleet.Environment(envName)
	if err != nil {
		return "", err
	}

	sites, err := f.Sites(env)
	if err != nil {
		return "", err
	}

	selected, err := fleet.Select(sites, []string{name})
	if err != nil {
		return "", fmt.Errorf("environment %s: %w", env.Name, err)
	}

	return selected[0].File(), nil
}

// checkDir fails unless path is a directory.
func checkDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("--source: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%w: %s", ErrNotDirectory, path)
	}

	return nil
}
