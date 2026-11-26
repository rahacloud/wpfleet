package fleet

import (
	"errors"
	"fmt"
	"regexp"
)

// Value paths in a release's values that the fleet model reads.
const (
	ValueSite            = "site"
	ValueDomains         = "domains"
	ValueDomainSuffix    = "domainSuffix"
	ValueHostLabelSuffix = "hostLabelSuffix"
	ValueImageRepository = "image.repository"
	ValueImageTag        = "image.tag"
)

// ErrNoHost is returned when the values give neither domains nor
// domainSuffix.
var ErrNoHost = errors.New("domains or domainSuffix is required")

// ErrInvalidValue is returned when a value breaks a rule the chart enforces.
var ErrInvalidValue = errors.New("invalid value")

// hostLabelSuffix is the chart's rule for hostLabelSuffix.
var hostLabelSuffix = regexp.MustCompile(`^[a-z0-9-]*$`)

// Values is what the fleet model needs from a site's merged values. The infra
// values package implements it; paths are dot-separated, e.g. image.tag.
type Values interface {
	// String returns the string at path, or "" when it is missing.
	String(path string) string
	// StringSlice returns the list of strings at path, or nil when it is
	// missing.
	StringSlice(path string) []string
}

// Host returns a site's canonical host from its merged values, exactly as the
// chart's wordpress.hosts helper derives it: the first of domains, else
// <site><hostLabelSuffix>.<domainSuffix>.
func Host(v Values) (string, error) {
	if domains := v.StringSlice(ValueDomains); len(domains) > 0 {
		return domains[0], nil
	}

	suffix := v.String(ValueDomainSuffix)
	if suffix == "" {
		return "", ErrNoHost
	}

	site := v.String(ValueSite)
	err := ValidateSiteName(site)
	if err != nil {
		return "", err
	}

	label := v.String(ValueHostLabelSuffix)
	if !hostLabelSuffix.MatchString(label) {
		return "", fmt.Errorf("%w: hostLabelSuffix %q must be lowercase letters, digits and hyphens", ErrInvalidValue, label)
	}

	return site + label + "." + suffix, nil
}

// Image returns the image.repository and image.tag of a site's values.
func Image(v Values) ImageRef {
	return ImageRef{Repository: v.String(ValueImageRepository), Tag: v.String(ValueImageTag), Digest: ""}
}
