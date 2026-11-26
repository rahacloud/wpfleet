// Package s3 lists the backups the chart's backup CronJob writes to an
// S3-compatible bucket, with minio-go. It only reads: the CronJob writes and
// expires the runs, and restores read them from inside the cluster.
package s3

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/rahacloud/wpfleet/internal/domain/backup"
)

// The environment variables that hold the keys. They are never read from
// wpfleet.yaml, so the fleet repository never holds them.
const (
	EnvAccessKeyID     = "WPFLEET_S3_ACCESS_KEY_ID"
	EnvSecretAccessKey = "WPFLEET_S3_SECRET_ACCESS_KEY" //nolint:gosec // the name of a variable, not a credential
)

// ErrNoCredentials is returned when the key variables are not set.
var ErrNoCredentials = errors.New("S3 keys are not set: " + EnvAccessKeyID + " and " + EnvSecretAccessKey)

// ErrNoBucket is returned when no bucket or endpoint is configured.
var ErrNoBucket = errors.New("backup.s3.endpoint and backup.s3.bucket are required in wpfleet.yaml")

// ErrInvalidEndpoint is returned for an endpoint that is not a bare http or
// https URL.
var ErrInvalidEndpoint = errors.New("backup.s3.endpoint must be an http or https URL without a path")

// Config says where the bucket is.
type Config struct {
	// Endpoint is the service's URL, e.g. https://s3.example.com. Without a
	// scheme, https is assumed.
	Endpoint string
	// Bucket is the bucket the CronJobs write to.
	Bucket string
	// Region is the bucket's region; empty asks the service.
	Region string
}

// Credentials are the keys of the bucket. Their String hides the secret.
type Credentials struct {
	// AccessKeyID is the access key.
	AccessKeyID string
	// SecretAccessKey is the secret key.
	SecretAccessKey string
}

// String shows the access key and hides the secret, so that a credential that
// ends up in a log or an error does not leak it.
func (c Credentials) String() string {
	return "access key " + c.AccessKeyID + ", secret hidden"
}

// CredentialsFromEnv reads the keys from WPFLEET_S3_ACCESS_KEY_ID and
// WPFLEET_S3_SECRET_ACCESS_KEY through getenv.
func CredentialsFromEnv(getenv func(string) string) (Credentials, error) {
	c := Credentials{AccessKeyID: getenv(EnvAccessKeyID), SecretAccessKey: getenv(EnvSecretAccessKey)}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return Credentials{}, ErrNoCredentials
	}

	return c, nil
}

// Client reads one bucket.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New makes a client for the bucket. It does not contact the service.
func New(cfg Config, creds Credentials) (*Client, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, ErrNoBucket
	}

	host, secure, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	//nolint:exhaustruct_v5 // minio's options are optional by design
	mc, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(creds.AccessKeyID, creds.SecretAccessKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("S3 client for %s: %w", cfg.Endpoint, err)
	}

	return &Client{mc: mc, bucket: cfg.Bucket}, nil
}

// parseEndpoint splits an endpoint URL into the host minio-go takes and
// whether it uses TLS.
func parseEndpoint(endpoint string) (string, bool, error) {
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrInvalidEndpoint, err)
	}

	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return "", false, fmt.Errorf("%w: %s", ErrInvalidEndpoint, endpoint)
	}

	return u.Host, u.Scheme == "https", nil
}

// Bucket is the bucket the client reads.
func (c *Client) Bucket() string {
	return c.bucket
}

// Backups lists the runs of the site whose directory in the bucket is prefix
// (backup.Prefix), newest first: the dumps under database/ with their sizes,
// and the number of files under each run's uploads/changed/<stamp>/. Objects
// that are not named by a stamp are ignored.
func (c *Client) Backups(ctx context.Context, prefix string) ([]backup.Backup, error) {
	dumps := map[backup.Stamp]int64{}

	dir := backup.DatabaseDir(prefix)

	err := c.list(ctx, dir, func(name string, size int64) {
		if s, ok := backup.StampOfDump(name); ok {
			dumps[s] = size
		}
	})
	if err != nil {
		return nil, err
	}

	changed := map[backup.Stamp]int{}

	err = c.list(ctx, backup.ChangedDir(prefix), func(name string, _ int64) {
		run, rest, ok := strings.Cut(name, "/")
		if !ok || rest == "" || strings.HasSuffix(rest, "/") {
			return
		}

		s, err := backup.ParseStamp(run)
		if err == nil {
			changed[s]++
		}
	})
	if err != nil {
		return nil, err
	}

	return backup.Merge(dumps, changed), nil
}

// list calls fn with the name (relative to dir) and size of every object
// under dir, recursively.
func (c *Client) list(ctx context.Context, dir string, fn func(name string, size int64)) error {
	//nolint:exhaustruct_v5 // a prefix and recursion are the only options needed
	opts := minio.ListObjectsOptions{Prefix: dir, Recursive: true}

	for obj := range c.mc.ListObjectsIter(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return fmt.Errorf("listing s3://%s/%s: %w", c.bucket, dir, obj.Err)
		}

		if name := strings.TrimPrefix(obj.Key, dir); name != "" {
			fn(name, obj.Size)
		}
	}

	return nil
}
