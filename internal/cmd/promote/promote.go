// Package promote is the promote command: it copies sites' images from the
// environment a target promotes from into the target's site files, keeping
// their comments and order. It does not deploy.
package promote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// flagTo names the target environment.
const flagTo = "to"

// Result is one site's promotion as promote prints it.
type Result struct {
	fleet.PromotionPlan

	// Changed says whether the target's site file changed.
	Changed bool `json:"changed"`
}

// Command is wpfleet promote.
func Command() *cli.Command {
	//nolint:exhaustruct_v5 // urfave/cli commands are configured by their set fields
	return &cli.Command{
		Name:      "promote",
		Usage:     "copy sites' images from the promoteFrom environment into --to",
		ArgsUsage: "<site>...",
		Description: "Copies image.repository and image.tag from each site's file in the environment " +
			"that --to promotes from into the site's file in --to, keeping comments and order. " +
			"It does not deploy; run deploy after it.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     flagTo,
				Usage:    "the environment to promote into",
				Required: true,
			},
		},
		Action: run,
	}
}

// run is the promote command's action. It plans every site before it writes
// any file, so a site without an image stops the whole promotion.
func run(ctx context.Context, cmd *cli.Command) error {
	names := unique(cmd.Args().Slice())
	if len(names) == 0 {
		return cmdutil.ErrNoSites
	}

	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	plans, files, err := plan(f, cmd.String(flagTo), names)
	if err != nil {
		return err
	}

	results := make([]Result, 0, len(plans))

	var errs []error

	for i, p := range plans {
		if p.Changed() {
			err := values.SetImage(files[i], p.Target.Repository, p.Target.Tag)
			if err != nil {
				errs = append(errs, err)

				continue
			}

			slog.DebugContext(ctx, "promoted", "site", p.Site, "image", p.Target.String(), "file", files[i])
		}

		results = append(results, Result{PromotionPlan: p, Changed: p.Changed()})
	}

	err = cmdutil.Print(cmd, results,
		cmdutil.Column[Result]{Header: "site", Value: func(r Result) string { return r.Site }},
		cmdutil.Column[Result]{Header: "from", Value: func(r Result) string { return r.From }},
		cmdutil.Column[Result]{Header: "to", Value: func(r Result) string { return r.To }},
		cmdutil.Column[Result]{Header: "image", Value: change},
	)

	return errors.Join(append(errs, err)...)
}

// plan loads the source and target site files of every named site and plans
// their promotion. It returns the plans and the target files to edit.
func plan(f *cmdutil.Fleet, to string, names []string) ([]fleet.PromotionPlan, []string, error) {
	target, err := f.Fleet.Environment(to)
	if err != nil {
		return nil, nil, err
	}

	source, err := f.Fleet.PromotionSource(to)
	if err != nil {
		return nil, nil, err
	}

	sourceSites, err := selectSites(f, source, names)
	if err != nil {
		return nil, nil, err
	}

	targetSites, err := selectSites(f, target, names)
	if err != nil {
		return nil, nil, err
	}

	plans := make([]fleet.PromotionPlan, 0, len(names))
	files := make([]string, 0, len(names))

	var errs []error

	for i, name := range names {
		p, err := planSite(name, sourceSites[i], targetSites[i])
		if err != nil {
			errs = append(errs, err)

			continue
		}

		plans = append(plans, p)
		files = append(files, targetSites[i].File())
	}

	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}

	return plans, files, nil
}

// planSite plans one site from its own files only: the image a promotion
// copies is the one the source's site file pins, not one from _shared.yaml.
func planSite(name string, source, target fleet.Site) (fleet.PromotionPlan, error) {
	sv, err := values.Load(source.File())
	if err != nil {
		return fleet.PromotionPlan{}, err
	}

	tv, err := values.Load(target.File())
	if err != nil {
		return fleet.PromotionPlan{}, err
	}

	return fleet.PlanPromotion(name, source.Env.Name, target.Env.Name, sv, tv)
}

// selectSites discovers the sites of env and picks the named ones.
func selectSites(f *cmdutil.Fleet, env fleet.Environment, names []string) ([]fleet.Site, error) {
	sites, err := f.Sites(env)
	if err != nil {
		return nil, err
	}

	selected, err := fleet.Select(sites, names)
	if err != nil {
		return nil, fmt.Errorf("environment %s: %w", env.Name, err)
	}

	return selected, nil
}

// unique drops repeated names, keeping the first of each.
func unique(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}

	return out
}

// change formats a result's image column: old -> new, or the image alone
// when it did not change.
func change(r Result) string {
	if !r.Changed {
		return r.Target.String() + " (unchanged)"
	}

	current := r.Current.String()
	if r.Current.IsZero() {
		current = "(none)"
	}

	return current + " -> " + r.Target.String()
}
