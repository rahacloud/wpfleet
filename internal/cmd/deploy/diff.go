package deploy

import (
	"context"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// DiffCommand returns wpfleet diff.
func DiffCommand() *cli.Command {
	return diffCommand(helmReleases)
}

// diffCommand builds wpfleet diff on the given Releases.
func diffCommand(releases releasesFunc) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "diff",
		Usage:     "show what a deploy would change; exit code 1 when it would change something",
		ArgsUsage: "<site>... | --all",
		Description: "Compares the manifest of each site's live release with the one its values render now, " +
			"hooks left out, ignoring blank lines and the order of the objects, and prints a unified diff. " +
			"A site without a release shows as entirely new. The values of Secrets are never printed: " +
			"each shows as a tag that changes when the value changes.",
		Flags: []cli.Flag{allFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runDiff(ctx, cmd, releases)
		},
	}
}

// Diff is what a deploy would change on one site.
type Diff = ops.SiteDiff

// runDiff diffs the selected sites. It prints the unified diffs, or with
// --output json one object per site, and returns exit code 1 when any site
// differs. A site that cannot be compared is reported and fails the command.
func runDiff(ctx context.Context, cmd *cli.Command, releasesOf releasesFunc) error {
	format, err := cmdutil.ParseFormat(cmd.String(cmdutil.FlagOutput))
	if err != nil {
		return err
	}

	env, sites, err := open(cmd)
	if err != nil {
		return err
	}

	releases, err := releasesOf(env)
	if err != nil {
		return err
	}

	report, err := ops.Diff(ctx, env, sites, ops.DiffOptions{Releases: releases})
	if err != nil {
		return err
	}

	err = writeDiffs(cmd.Root().Writer, cmd.Root().ErrWriter, format, report.Diffs)
	if err != nil {
		return err
	}

	err = report.Err()
	if err != nil {
		return err
	}

	if report.Changed() {
		return cli.Exit("", 1) //nolint:wrapcheck // an exit code without a message, by design
	}

	return nil
}

// writeDiffs prints the diffs: the unified diffs one after another on out
// and the sites that failed on errOut, or every site as JSON on out.
func writeDiffs(out, errOut io.Writer, format cmdutil.Format, diffs []Diff) error {
	if format == cmdutil.FormatJSON {
		return cmdutil.Write(out, format, diffs)
	}

	for _, d := range diffs {
		if d.Error != "" {
			_, _ = fmt.Fprintf(errOut, "%s/%s: %s\n", d.Env, d.Site, d.Error)

			continue
		}

		_, err := io.WriteString(out, d.Diff)
		if err != nil {
			return fmt.Errorf("writing diff: %w", err)
		}
	}

	return nil
}
