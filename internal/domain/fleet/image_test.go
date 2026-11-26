package fleet_test

import (
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func ref(repository, tag, digest string) fleet.ImageRef {
	return fleet.ImageRef{Repository: repository, Tag: tag, Digest: digest}
}

func TestParseImageRef(t *testing.T) {
	t.Parallel()

	const withPort = "registry.example.com:5000/sites/blog"

	tests := []struct {
		in   string
		want fleet.ImageRef
	}{
		{"docker.io/library/wordpress:7.0.2-php8.4-apache", ref("docker.io/library/wordpress", "7.0.2-php8.4-apache", "")},
		{withPort + ":20261126-101500", ref(withPort, "20261126-101500", "")},
		{withPort, ref(withPort, "", "")},
		{"wordpress", ref("wordpress", "", "")},
		{"blog:1@sha256:abc", ref(blog, "1", "sha256:abc")},
		{"blog@sha256:abc", ref(blog, "", "sha256:abc")},
	}

	for _, tt := range tests {
		got, err := fleet.ParseImageRef(tt.in)
		testutil.NoError(t, err)
		testutil.Equal(t, "parse "+tt.in, got, tt.want)
		testutil.Equal(t, "format", got.String(), tt.in)
	}

	for _, bad := range []string{"", "blog:", ":1", "blog@", "registry.example.com/", "a b"} {
		_, err := fleet.ParseImageRef(bad)
		testutil.ErrorIs(t, err, fleet.ErrInvalidImage)
	}

	testutil.True(t, "zero is zero", ref("", "", "").IsZero())
	testutil.True(t, "non-zero", !ref(blog, "", "").IsZero())
}
