package ops

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/infra/values"
)

// Promotion is a planned promotion of sites into one environment: what each
// site's image becomes, and the edit of each target site file that changes.
// Nothing is written until ApplyPromotion.
type Promotion struct {
	// To is the target environment.
	To string
	// From is the environment it promotes from.
	From string
	// Plans has one plan per site, in the order named.
	Plans []fleet.PromotionPlan

	edits []values.ImageEdit
}

// Files are the target site files the promotion changes, the ones a commit
// of it holds.
func (p Promotion) Files() []string {
	var files []string

	for i, plan := range p.Plans {
		if plan.Changed() {
			files = append(files, p.edits[i].Path)
		}
	}

	return files
}

// Changed says whether the promotion changes any file.
func (p Promotion) Changed() bool {
	return slices.ContainsFunc(p.Plans, fleet.PromotionPlan.Changed)
}

// PromoteResult is one site's promotion.
type PromoteResult struct {
	fleet.PromotionPlan

	// Changed says whether the target's site file changed.
	Changed bool `json:"changed"`
	// File is the target site file.
	File string `json:"-"`
}

// PlanPromotion plans the promotion of the named sites into the environment
// to, from the environment it promotes from. It reads both site files of
// every site (only the site files count: an image from _shared.yaml is not
// promoted) and computes the edit of every target file that changes, so that
// one site that cannot be promoted refuses the whole promotion before any
// file is written. Repeated names count once.
func PlanPromotion(f *Fleet, to string, names []string) (Promotion, error) {
	names = unique(names)
	if len(names) == 0 {
		return Promotion{}, ErrNoSites
	}

	target, err := f.Fleet.Environment(to)
	if err != nil {
		return Promotion{}, err
	}

	source, err := f.Fleet.PromotionSource(to)
	if err != nil {
		return Promotion{}, err
	}

	sourceSites, err := selectSites(f, source, names)
	if err != nil {
		return Promotion{}, err
	}

	targetSites, err := selectSites(f, target, names)
	if err != nil {
		return Promotion{}, err
	}

	p := Promotion{
		To: target.Name, From: source.Name,
		Plans: make([]fleet.PromotionPlan, 0, len(names)), edits: make([]values.ImageEdit, 0, len(names)),
	}

	var errs []error

	for i, name := range names {
		plan, edit, err := planEdit(name, sourceSites[i], targetSites[i])
		if err != nil {
			errs = append(errs, err)

			continue
		}

		p.Plans = append(p.Plans, plan)
		p.edits = append(p.edits, edit)
	}

	if len(errs) > 0 {
		return Promotion{}, errors.Join(errs...)
	}

	return p, nil
}

// planEdit plans one site and, when its image changes, the edit of its
// target site file.
func planEdit(name string, source, target fleet.Site) (fleet.PromotionPlan, values.ImageEdit, error) {
	edit := values.ImageEdit{Path: target.File(), Before: nil, After: nil}

	plan, err := planSite(name, source, target)
	if err != nil || !plan.Changed() {
		return plan, edit, err
	}

	edit, err = values.PlanImage(target.File(), plan.Target.Repository, plan.Target.Tag)

	return plan, edit, err
}

// ApplyPromotion writes the edit of every site whose image changes; an
// unchanged site's file is not touched. A file that changed on disk since it
// was planned is refused (values.ErrChangedOnDisk) and left alone, and the
// other sites are still written: the results hold every site that was
// written or unchanged, and the error joins the refusals.
func ApplyPromotion(ctx context.Context, p Promotion) ([]PromoteResult, error) {
	results := make([]PromoteResult, 0, len(p.Plans))

	var errs []error

	for i, plan := range p.Plans {
		if plan.Changed() {
			err := p.edits[i].Write()
			if err != nil {
				errs = append(errs, err)

				continue
			}

			slog.DebugContext(ctx, "promoted", "site", plan.Site, "image", plan.Target.String(), "file", p.edits[i].Path)
		}

		results = append(results, PromoteResult{PromotionPlan: plan, Changed: plan.Changed(), File: p.edits[i].Path})
	}

	return results, errors.Join(errs...)
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
func selectSites(f *Fleet, env fleet.Environment, names []string) ([]fleet.Site, error) {
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
