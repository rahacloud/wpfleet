package backup

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Value paths in a site's merged values (chart defaults included) that a
// restore reads.
const (
	ValueSite          = "site"
	ValueDBHost        = "database.host"
	ValueDBPort        = "database.port"
	ValueDBName        = "database.name"
	ValueDBUser        = "database.user"
	ValueMariaDBImage  = "database.provision.image"
	ValueRcloneImage   = "backup.image"
	ValueS3Endpoint    = "backup.s3.endpoint"
	ValueS3Bucket      = "backup.s3.bucket"
	ValueS3Secret      = "backup.s3.secretName"
	ValueKeepDays      = "backup.keepDays"
	ValueExistingClaim = "uploads.existingClaim"
	ValueConfigExtra   = "wordpress.configExtra"
)

// maxFullName is the chart's cut of the site name in object names.
const maxFullName = 50

// DBPasswordKey is the key of the database password in the site's Secret.
const DBPasswordKey = "WORDPRESS_DB_PASSWORD" //nolint:gosec // the name of a Secret key, not a credential

// ErrMissingValue is returned when the values lack something a restore
// needs.
var ErrMissingValue = errors.New("missing value")

// dbIdentRule is the chart's rule for database and user names.
var dbIdentRule = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// Values is what this package reads from a site's merged values.
type Values interface {
	// String returns the scalar at a dot-separated path, or "".
	String(path string) string
}

// Site is what a restore needs to know about the site it writes into,
// derived from its values exactly as the chart's helpers derive it.
type Site struct {
	// Name is the site value.
	Name string `json:"name"`
	// FullName is the chart's wordpress.fullname: the site cut to 50.
	FullName string `json:"fullName"`
	// Secret is the site's Secret, which holds DBPasswordKey.
	Secret string `json:"secret"`
	// Claim is the uploads PersistentVolumeClaim.
	Claim string `json:"claim"`
	// DBHost, DBPort, DBName and DBUser are where the site's database is and
	// who it connects as.
	DBHost string `json:"dbHost"`
	DBPort string `json:"dbPort"`
	DBName string `json:"dbName"`
	DBUser string `json:"dbUser"`
	// MariaDBImage is database.provision.image, which has the mariadb client.
	MariaDBImage string `json:"mariadbImage"`
	// RcloneImage is backup.image.
	RcloneImage string `json:"rcloneImage"`
	// S3Endpoint is backup.s3.endpoint, the bucket as the cluster reaches it.
	S3Endpoint string `json:"s3Endpoint"`
	// S3Secret is the Secret in the namespace with the bucket's keys.
	S3Secret string `json:"s3Secret"`
}

// SiteOf derives the restore's view of a site from its merged values, chart
// defaults included.
func SiteOf(v Values) (Site, error) {
	name := v.String(ValueSite)
	if name == "" {
		return Site{}, fmt.Errorf("%w: %s", ErrMissingValue, ValueSite)
	}

	full := name[:min(len(name), maxFullName)]
	ident := "wp_" + strings.ReplaceAll(name, "-", "_")

	s := Site{
		Name:         name,
		FullName:     full,
		Secret:       full,
		Claim:        cmp.Or(v.String(ValueExistingClaim), full+"-uploads"),
		DBHost:       v.String(ValueDBHost),
		DBPort:       v.String(ValueDBPort),
		DBName:       cmp.Or(v.String(ValueDBName), ident),
		DBUser:       cmp.Or(v.String(ValueDBUser), ident),
		MariaDBImage: v.String(ValueMariaDBImage),
		RcloneImage:  v.String(ValueRcloneImage),
		S3Endpoint:   v.String(ValueS3Endpoint),
		S3Secret:     v.String(ValueS3Secret),
	}

	var errs []error

	for _, required := range [][2]string{
		{ValueDBHost, s.DBHost}, {ValueDBPort, s.DBPort}, {ValueMariaDBImage, s.MariaDBImage},
		{ValueRcloneImage, s.RcloneImage}, {ValueS3Secret, s.S3Secret},
	} {
		if required[1] == "" {
			errs = append(errs, fmt.Errorf("%w: %s", ErrMissingValue, required[0]))
		}
	}

	if !dbIdentRule.MatchString(s.DBName) || !dbIdentRule.MatchString(s.DBUser) {
		errs = append(errs, fmt.Errorf("%w: database name and user must be letters, digits and underscores", ErrMissingValue))
	}

	return s, errors.Join(errs...)
}

// Multisite reports whether wordpress.configExtra turns on multisite, which
// makes copy run search-replace with --network.
func Multisite(v Values) bool {
	return strings.Contains(v.String(ValueConfigExtra), "MULTISITE")
}
