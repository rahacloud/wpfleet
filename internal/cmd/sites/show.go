package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// ErrOneSite is returned when sites show is not given exactly one site.
var ErrOneSite = errors.New("sites show takes exactly one site")

// timeLayout is how sites show prints a time, always in UTC.
const timeLayout = "2006-01-02 15:04:05 UTC"

// Detail is one site in one environment, as sites show prints it.
type Detail = ops.SiteDetail

// showCommand builds wpfleet sites show.
func showCommand(d deps) *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "show",
		Usage:     "one site of --env: host, image, release, pods, node, volume and last backup",
		ArgsUsage: "<site>",
		Description: "The last backup is the last successful run of the site's backup CronJob; " +
			"wpfleet backup list shows the backups in S3.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runShow(ctx, cmd, d)
		},
	}
}

// runShow shows one site.
func runShow(ctx context.Context, cmd *cli.Command, d deps) error {
	if cmd.Args().Len() != 1 {
		return ErrOneSite
	}

	format, err := cmdutil.ParseFormat(cmd.String(cmdutil.FlagOutput))
	if err != nil {
		return err
	}

	env, err := cmdutil.OpenEnv(cmd)
	if err != nil {
		return err
	}

	site, err := env.Site(cmd.Args().First())
	if err != nil {
		return err
	}

	opts, err := d.showOptions(env)
	if err != nil {
		return err
	}

	detail, err := ops.ShowSite(ctx, env, site, opts)
	if err != nil {
		return err
	}

	w := cmd.Root().Writer
	if format == cmdutil.FormatJSON {
		return writeJSON(w, detail)
	}

	return cmdutil.Write(w, format, fields(detail), fieldColumns()...)
}

// showOptions resolves the replaced cluster and releases, if any.
func (d deps) showOptions(env *cmdutil.Env) (ops.ShowOptions, error) {
	opts := ops.ShowOptions{Cluster: nil, Releases: nil}

	var err error

	if d.releases != nil {
		opts.Releases, err = d.releases(env)
		if err != nil {
			return opts, err
		}
	}

	if d.cluster != nil {
		opts.Cluster, err = d.cluster(env)
		if err != nil {
			return opts, err
		}
	}

	return opts, nil
}

// field is one line of sites show's table.
type field struct {
	name, value string
}

// fieldColumns are the columns of sites show's table.
func fieldColumns() []cmdutil.Column[field] {
	return []cmdutil.Column[field]{
		{Header: "field", Value: func(f field) string { return f.name }},
		{Header: "value", Value: func(f field) string { return f.value }},
	}
}

// fields lays the detail out as lines for people.
func fields(d Detail) []field {
	return []field{
		{"site", d.Site},
		{"env", d.Env},
		{"namespace", d.Namespace},
		{"host", d.Host},
		{"image", d.Image},
		{"release", releaseText(d)},
		{"pods", podsText(d)},
		{"node", nodesText(d)},
		{"volume", volumeText(d.Volume)},
		{"last backup", backupText(d.Backup)},
	}
}

// releaseText describes the release.
func releaseText(d Detail) string {
	r := d.Release
	if r == nil {
		return "not installed"
	}

	return fmt.Sprintf("revision %d, %s, chart %s, %s",
		r.Revision, r.Status, r.ChartVersion, r.Updated.UTC().Format(timeLayout))
}

// podsText describes the pods.
func podsText(d Detail) string {
	if len(d.Pods) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(d.Pods))

	for _, p := range d.Pods {
		state := "not ready"
		if p.Ready {
			state = "ready"
		}

		parts = append(parts, fmt.Sprintf("%s (%s, %s)", p.Name, p.Phase, state))
	}

	return strings.Join(parts, "; ")
}

// nodesText lists the nodes the pods run on.
func nodesText(d Detail) string {
	var nodes []string

	for _, p := range d.Pods {
		if p.Node != "" && !slices.Contains(nodes, p.Node) {
			nodes = append(nodes, p.Node)
		}
	}

	return strings.Join(nodes, ", ")
}

// volumeText describes the claim.
func volumeText(v ops.SiteVolume) string {
	if !v.Found {
		return v.Claim + " (not found)"
	}

	parts := slices.DeleteFunc([]string{v.Phase, v.Capacity, v.StorageClass}, func(s string) bool { return s == "" })

	return v.Claim + " (" + strings.Join(parts, ", ") + ")"
}

// backupText describes the last backup.
func backupText(b ops.SiteBackup) string {
	switch {
	case !b.Enabled:
		return "disabled"
	case !b.Found:
		return "no CronJob " + b.CronJob
	case b.LastSuccess == nil:
		return "never"
	default:
		return b.LastSuccess.Format(timeLayout)
	}
}

// writeJSON writes the detail as one indented JSON object.
func writeJSON(w io.Writer, d Detail) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(d)
	if err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}
