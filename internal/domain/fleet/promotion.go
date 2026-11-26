package fleet

import (
	"errors"
	"fmt"
)

// ErrNoImage is returned when a source site's values have no image to
// promote.
var ErrNoImage = errors.New("no image.repository and image.tag to promote")

// PromotionPlan is what promoting one site from one environment to the next
// changes: the target's image becomes the source's.
type PromotionPlan struct {
	// Site is the site's name.
	Site string `json:"site"`
	// From is the source environment's name.
	From string `json:"from"`
	// To is the target environment's name.
	To string `json:"to"`
	// Current is the image the target runs now; empty when it has none.
	Current ImageRef `json:"current"`
	// Target is the image the target will run, the source's.
	Target ImageRef `json:"target"`
}

// PlanPromotion compares the images in the source and target site values of a
// site and returns the plan. The source must have both a repository and a
// tag.
func PlanPromotion(site, from, to string, source, target Values) (PromotionPlan, error) {
	img := Image(source)
	if img.Repository == "" || img.Tag == "" {
		return PromotionPlan{}, fmt.Errorf("%s in %s: %w", site, from, ErrNoImage)
	}

	return PromotionPlan{Site: site, From: from, To: to, Current: Image(target), Target: img}, nil
}

// Changed reports whether the promotion changes the target's image.
func (p PromotionPlan) Changed() bool {
	return p.Current != p.Target
}
