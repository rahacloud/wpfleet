package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/helm"
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
type Diff struct {
	// Site is the site's name.
	Site string `json:"site"`
	// Env is the environment.
	Env string `json:"env"`
	// Installed says whether the site has a release.
	Installed bool `json:"installed"`
	// Changed says whether a deploy would change anything.
	Changed bool `json:"changed"`
	// Diff is the unified diff from the live manifest to the rendered one.
	Diff string `json:"diff,omitempty"`
	// Error is why the site could not be compared, when it could not.
	Error string `json:"error,omitempty"`
}

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

	r := newRedactor()
	diffs := make([]Diff, 0, len(sites))

	var failed []string

	for _, site := range sites {
		d := diffSite(ctx, releases, r, env.Env.Name, site)
		if d.Error != "" {
			failed = append(failed, site.Name)
		}

		diffs = append(diffs, d)
	}

	err = writeDiffs(cmd.Root().Writer, cmd.Root().ErrWriter, format, diffs)
	if err != nil {
		return err
	}

	err = failure(failed, len(sites))
	if err != nil {
		return err
	}

	for _, d := range diffs {
		if d.Changed {
			return cli.Exit("", 1) //nolint:wrapcheck // an exit code without a message, by design
		}
	}

	return nil
}

// diffSite compares one site's live release with its rendering.
func diffSite(ctx context.Context, releases Releases, r redactor, envName string, site fleet.Site) Diff {
	d := Diff{Site: site.Name, Env: envName, Installed: true, Changed: false, Diff: "", Error: ""}

	rendered, err := releases.Render(ctx, site.Name, site.Env.Namespace, site.ValuesFiles)
	if err != nil {
		d.Error = err.Error()

		return d
	}

	live, err := releases.LiveManifest(site.Name, site.Env.Namespace)

	switch {
	case errors.Is(err, helm.ErrReleaseNotFound):
		d.Installed, live = false, ""
	case err != nil:
		d.Error = err.Error()

		return d
	}

	prefix := envName + "/" + site.Name

	d.Diff, err = unifiedDiff(r.normalize(live), r.normalize(rendered), prefix+" (live)", prefix+" (rendered)")
	if err != nil {
		d.Error = err.Error()

		return d
	}

	d.Changed = d.Diff != ""

	return d
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
