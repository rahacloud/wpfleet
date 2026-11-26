package fleet

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidImage is returned for an image reference that cannot be parsed.
var ErrInvalidImage = errors.New("invalid image reference")

// ImageRef is a container image reference split the way the chart's values
// hold it: image.repository and image.tag. Digest is kept when a reference
// carries one, though site files pin by tag.
type ImageRef struct {
	// Repository is the image name with its registry, e.g.
	// registry.example.com/sites/blog.
	Repository string `json:"repository"`
	// Tag is the tag, e.g. 20261126-101500.
	Tag string `json:"tag"`
	// Digest is the content digest, e.g. sha256:..., when the reference has
	// one.
	Digest string `json:"digest,omitempty"`
}

// ParseImageRef splits a reference such as
// registry.example.com:5000/sites/blog:20261126-101500 into its repository,
// tag and digest. A port in the registry is not mistaken for a tag.
func ParseImageRef(ref string) (ImageRef, error) {
	if ref == "" || strings.ContainsAny(ref, " \t\n") {
		return ImageRef{}, fmt.Errorf("%w: %q", ErrInvalidImage, ref)
	}

	name, digest, _ := strings.Cut(ref, "@")

	repo, tag := name, ""

	slash := strings.LastIndex(name, "/")
	if colon := strings.LastIndex(name, ":"); colon > slash {
		repo, tag = name[:colon], name[colon+1:]
		if tag == "" {
			return ImageRef{}, fmt.Errorf("%w: %q has an empty tag", ErrInvalidImage, ref)
		}
	}

	if repo == "" || strings.HasSuffix(repo, "/") {
		return ImageRef{}, fmt.Errorf("%w: %q has no repository", ErrInvalidImage, ref)
	}

	if strings.Contains(ref, "@") && digest == "" {
		return ImageRef{}, fmt.Errorf("%w: %q has an empty digest", ErrInvalidImage, ref)
	}

	return ImageRef{Repository: repo, Tag: tag, Digest: digest}, nil
}

// String formats the reference as repository[:tag][@digest].
func (r ImageRef) String() string {
	var b strings.Builder

	b.WriteString(r.Repository)

	if r.Tag != "" {
		b.WriteString(":" + r.Tag)
	}

	if r.Digest != "" {
		b.WriteString("@" + r.Digest)
	}

	return b.String()
}

// IsZero reports whether the reference is empty.
func (r ImageRef) IsZero() bool {
	return r.Repository == "" && r.Tag == "" && r.Digest == ""
}
