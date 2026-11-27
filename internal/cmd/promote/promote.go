// Package promote is the promote command: it copies sites' images from the
// environment a target promotes from into the target's site files, keeping
// their comments and order. It does not deploy.
package promote

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
	"github.com/rahacloud/wpfleet/internal/ops"
)

// flagTo names the target environment.
const flagTo = "to"

// Result is one site's promotion as promote prints it.
type Result = ops.PromoteResult

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
	if len(cmd.Args().Slice()) == 0 {
		return ops.ErrNoSites
	}

	f, err := cmdutil.LoadFleet(cmd)
	if err != nil {
		return err
	}

	p, err := ops.PlanPromotion(f, cmd.String(flagTo), cmd.Args().Slice())
	if err != nil {
		return err
	}

	results, applyErr := ops.ApplyPromotion(ctx, p)

	err = cmdutil.Print(cmd, results,
		cmdutil.Column[Result]{Header: "site", Value: func(r Result) string { return r.Site }},
		cmdutil.Column[Result]{Header: "from", Value: func(r Result) string { return r.From }},
		cmdutil.Column[Result]{Header: "to", Value: func(r Result) string { return r.To }},
		cmdutil.Column[Result]{Header: "image", Value: change},
	)

	return errors.Join(applyErr, err)
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
