package backup_test

import (
	"strings"
	"testing"

	"github.com/rahacloud/wpfleet/internal/domain/backup"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

type vals map[string]string

func (v vals) String(path string) string { return v[path] }

func chartDefaults() vals {
	return vals{
		backup.ValueDBHost:       "mariadb",
		backup.ValueDBPort:       "3306",
		backup.ValueMariaDBImage: "docker.io/library/mariadb:12.3.3",
		backup.ValueRcloneImage:  "docker.io/rclone/rclone:1.72.0",
		backup.ValueS3Secret:     "backup-s3",
	}
}

func TestSiteOf(t *testing.T) {
	t.Parallel()

	v := chartDefaults()
	v[backup.ValueSite] = "my-blog"

	s, err := backup.SiteOf(v)
	testutil.NoError(t, err)
	testutil.Equal(t, "full name", s.FullName, "my-blog")
	testutil.Equal(t, "claim", s.Claim, "my-blog-uploads")
	testutil.Equal(t, "db name", s.DBName, "wp_my_blog")
	testutil.Equal(t, "db user", s.DBUser, "wp_my_blog")

	long := strings.Repeat("a", 60)
	v[backup.ValueSite] = long
	v[backup.ValueDBName] = "blog_db"
	v[backup.ValueDBUser] = "blog_user"
	v[backup.ValueExistingClaim] = "shared-uploads"

	s, err = backup.SiteOf(v)
	testutil.NoError(t, err)
	testutil.Equal(t, "cut", s.FullName, strings.Repeat("a", 50))
	testutil.Equal(t, "secret", s.Secret, strings.Repeat("a", 50))
	testutil.Equal(t, "existing claim", s.Claim, "shared-uploads")
	testutil.Equal(t, "db", s.DBName+" "+s.DBUser, "blog_db blog_user")
}

func TestSiteOfMissing(t *testing.T) {
	t.Parallel()

	_, err := backup.SiteOf(vals{})
	testutil.ErrorIs(t, err, backup.ErrMissingValue)

	_, err = backup.SiteOf(vals{backup.ValueSite: "blog"})
	testutil.ErrorIs(t, err, backup.ErrMissingValue)
	testutil.Contains(t, "error", err.Error(), backup.ValueMariaDBImage, backup.ValueS3Secret)

	v := chartDefaults()
	v[backup.ValueSite] = "blog"
	v[backup.ValueDBName] = "blog; DROP"

	_, err = backup.SiteOf(v)
	testutil.ErrorIs(t, err, backup.ErrMissingValue)
}

func TestMultisite(t *testing.T) {
	t.Parallel()

	testutil.True(t, "single", !backup.Multisite(vals{}))
	testutil.True(t, "multi", backup.Multisite(vals{backup.ValueConfigExtra: "define('MULTISITE', true);"}))
}
