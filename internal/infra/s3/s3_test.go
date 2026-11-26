package s3_test

import (
	"fmt"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/infra/s3"
	"github.com/rahacloud/wpfleet/internal/infra/s3/s3test"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

func creds() s3.Credentials {
	return s3.Credentials{AccessKeyID: s3test.AccessKeyID, SecretAccessKey: s3test.SecretAccessKey}
}

func TestBackups(t *testing.T) {
	t.Parallel()

	srv := s3test.New(t, "wordpress-backups", map[string]int64{
		"wp-staging/blog/database/20261125T031500Z.sql.gz":                     1200,
		"wp-staging/blog/database/20261126T031500Z.sql.gz":                     1300,
		"wp-staging/blog/database/notes.txt":                                   5,
		"wp-staging/blog/uploads/current/2026/11/a.jpg":                        10,
		"wp-staging/blog/uploads/changed/20261126T031500Z/2026/11/a.jpg":       9,
		"wp-staging/blog/uploads/changed/20261126T031500Z/2026/11/b.jpg":       9,
		"wp-staging/blog/uploads/changed/20261126T031500Z/2026/11/":            0,
		"wp-staging/blog/uploads/changed/not-a-stamp/c.jpg":                    1,
		"wp-staging/blog-old/database/20261126T031500Z.sql.gz":                 7,
		"wp-production/blog/database/20261126T031500Z.sql.gz":                  8,
		"wp-staging/blog/uploads/changed/20261124T031500Z/2026/10/removed.png": 3,
	})

	c, err := s3.New(s3.Config{Endpoint: srv.URL, Bucket: "wordpress-backups", Region: ""}, creds())
	testutil.NoError(t, err)
	testutil.Equal(t, "bucket", c.Bucket(), "wordpress-backups")

	got, err := c.Backups(t.Context(), backup.Prefix("wp-staging", "blog"))
	testutil.NoError(t, err)

	want := []string{
		"20261126T031500Z 1300 true 2",
		"20261125T031500Z 1200 true 0",
		"20261124T031500Z 0 false 1",
	}
	testutil.Equal(t, "runs", len(got), len(want))

	for i, b := range got {
		line := fmt.Sprintf("%s %d %t %d", b.Stamp, b.DumpSize, b.HasDump, b.ChangedFiles)
		testutil.Equal(t, "run", line, want[i])
	}

	testutil.Equal(t, "two listings", srv.Lists(), 2)

	none, err := c.Backups(t.Context(), backup.Prefix("wp-staging", "shop"))
	testutil.NoError(t, err)
	testutil.Equal(t, "no runs", len(none), 0)
}

func TestBackupsWrongKey(t *testing.T) {
	t.Parallel()

	srv := s3test.New(t, "wordpress-backups", nil)

	c, err := s3.New(s3.Config{Endpoint: srv.URL, Bucket: "wordpress-backups", Region: "us-east-1"},
		s3.Credentials{AccessKeyID: "other", SecretAccessKey: "x"})
	testutil.NoError(t, err)

	_, err = c.Backups(t.Context(), "wp-staging/blog")
	testutil.Error(t, err, "a refused listing")
	testutil.Contains(t, "error", err.Error(), "s3://wordpress-backups/wp-staging/blog/database/")
}

func TestNew(t *testing.T) {
	t.Parallel()

	_, err := s3.New(s3.Config{Endpoint: "", Bucket: "b", Region: ""}, creds())
	testutil.ErrorIs(t, err, s3.ErrNoBucket)

	for _, bad := range []string{"ftp://s3.example.com", "https://s3.example.com/bucket", "https://"} {
		_, err = s3.New(s3.Config{Endpoint: bad, Bucket: "b", Region: ""}, creds())
		testutil.ErrorIs(t, err, s3.ErrInvalidEndpoint)
	}

	_, err = s3.New(s3.Config{Endpoint: "s3.example.com", Bucket: "b", Region: ""}, creds())
	testutil.NoError(t, err)
}

func TestCredentialsFromEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{s3.EnvAccessKeyID: "AKID"}

	_, err := s3.CredentialsFromEnv(func(k string) string { return env[k] })
	testutil.ErrorIs(t, err, s3.ErrNoCredentials)

	env[s3.EnvSecretAccessKey] = "very-secret"

	c, err := s3.CredentialsFromEnv(func(k string) string { return env[k] })
	testutil.NoError(t, err)
	testutil.Equal(t, "key", c.AccessKeyID, "AKID")
	testutil.NotContains(t, "string", c.String(), "very-secret")
}
