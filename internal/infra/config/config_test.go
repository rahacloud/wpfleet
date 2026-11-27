package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rahacloud/wpfleet/internal/infra/config"
	"github.com/rahacloud/wpfleet/internal/testutil"
)

const sample = `# A fleet for tests.
registry: registry.example.com/sites
wpcli:
  sha512: abc
environments:
  - name: development
    namespace: wp-development
    kubeconfig: kube/dev.yaml
    context: dev
  - name: staging
    namespace: wp-staging
    values: values/staging
    promoteFrom: development
backup:
  s3:
    endpoint: https://s3.example.com
    bucket: wordpress-backups
`

func writeFleet(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	testutil.WriteFile(t, dir, config.FileName, content)

	return dir
}

func noEnv() []string { return nil }

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := writeFleet(t, sample)

	cfg, err := config.Load(dir, noEnv)
	testutil.NoError(t, err)

	testutil.Equal(t, "registry", cfg.Registry, "registry.example.com/sites")
	testutil.Equal(t, "sha512", cfg.WPCLI.SHA512, "abc")
	testutil.Equal(t, "bucket", cfg.Backup.S3.Bucket, "wordpress-backups")

	// Defaults survive the keys the file does not set.
	testutil.Equal(t, "wpcli url", cfg.WPCLI.URL, config.Default().WPCLI.URL)
	testutil.Equal(t, "build base", cfg.Build.Base, config.Default().Build.Base)

	testutil.Equal(t, "environments", len(cfg.Environments), 2)
	testutil.Equal(t, "promoteFrom", cfg.Environments[1].PromoteFrom, "development")
	testutil.Equal(t, "context", cfg.Environments[0].Context, "dev")
}

func TestFleet(t *testing.T) {
	t.Parallel()

	dir := writeFleet(t, sample)

	cfg, err := config.Load(dir, noEnv)
	testutil.NoError(t, err)

	f := cfg.Fleet(dir)

	testutil.Equal(t, "default values dir", f.Environments[0].ValuesDir, filepath.Join(dir, "environments/development"))
	testutil.Equal(t, "values dir", f.Environments[1].ValuesDir, filepath.Join(dir, "values/staging"))
	testutil.Equal(t, "kubeconfig", f.Environments[0].Kubeconfig, filepath.Join(dir, "kube/dev.yaml"))
	testutil.Equal(t, "empty kubeconfig", f.Environments[1].Kubeconfig, "")
	testutil.Equal(t, "registry", f.Registry, cfg.Registry)

	abs := config.Config{
		Chart:    "",
		Registry: "",
		Build:    config.Build{Base: ""},
		WPCLI:    config.WPCLI{URL: "", SHA512: "", AllowUnverified: false},
		Environments: []config.Environment{{
			Name: "a", Namespace: "x", Kubeconfig: "/etc/kube", Context: "", Values: "/srv/a", PromoteFrom: "",
		}},
		Backup: config.Backup{S3: config.S3{Endpoint: "", Bucket: "", Region: ""}},
		Server: config.DefaultServer(),
	}
	testutil.Equal(t, "absolute values dir", abs.Fleet(dir).Environments[0].ValuesDir, "/srv/a")
	testutil.Equal(t, "absolute kubeconfig", abs.Fleet(dir).Environments[0].Kubeconfig, "/etc/kube")
}

func TestLoadEnvironment(t *testing.T) {
	t.Parallel()

	dir := writeFleet(t, sample)

	cfg, err := config.Load(dir, func() []string {
		return []string{
			"WPFLEET_REGISTRY=registry.example.org/other",
			"WPFLEET_BACKUP__S3__BUCKET=from-env",
			"WPFLEET_BUILD__BASE=docker.io/library/wordpress:test",
			"WPFLEET_ENV=staging",
			"WPFLEET_S3_SECRET_ACCESS_KEY=never-a-key",
			"HOME=/root",
		}
	})
	testutil.NoError(t, err)

	testutil.Equal(t, "registry", cfg.Registry, "registry.example.org/other")
	testutil.Equal(t, "bucket", cfg.Backup.S3.Bucket, "from-env")
	testutil.Equal(t, "base", cfg.Build.Base, "docker.io/library/wordpress:test")
	testutil.Equal(t, "file value kept", cfg.Backup.S3.Endpoint, "https://s3.example.com")
}

func TestLoadMissing(t *testing.T) {
	t.Parallel()

	_, err := config.Load(t.TempDir(), noEnv)
	testutil.ErrorIs(t, err, os.ErrNotExist)

	_, err = config.Load(writeFleet(t, "environments: [\n"), noEnv)
	testutil.Error(t, err, "bad YAML")
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"listed twice": `environments:
  - {name: a, namespace: x}
  - {name: a, namespace: y}`,
		"has no namespace": `environments:
  - {name: a}`,
		"has no name": `environments:
  - {namespace: x}`,
		"promotes from unknown": `environments:
  - {name: a, namespace: x, promoteFrom: b}`,
		"promotes from itself": `environments:
  - {name: a, namespace: x, promoteFrom: a}`,
	}

	for want, content := range tests {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(writeFleet(t, content), noEnv)
			testutil.ErrorIs(t, err, config.ErrInvalid)
			testutil.Contains(t, "error", err.Error(), want)
		})
	}
}

// The default phar is checked against wp-cli's published sum, and running an
// unchecked one takes an explicit opt-out.
func TestDefaultWPCLIIsVerified(t *testing.T) {
	t.Parallel()

	def := config.Default().WPCLI
	testutil.Equal(t, "url", def.URL, config.DefaultWPCLIURL)
	testutil.Equal(t, "sum", def.SHA512, config.DefaultWPCLISHA512)
	testutil.Equal(t, "sum length", len(def.SHA512), 128)
	testutil.True(t, "verified by default", !def.AllowUnverified)

	dir := writeFleet(t,
		"wpcli:\n  url: https://mirror.example.com/wp-cli.phar\n  sha512: \"\"\n  allowUnverified: true\n")

	cfg, err := config.Load(dir, noEnv)
	testutil.NoError(t, err)
	testutil.Equal(t, "no sum", cfg.WPCLI.SHA512, "")
	testutil.True(t, "opted out", cfg.WPCLI.AllowUnverified)
}

func TestReservedEnv(t *testing.T) {
	t.Parallel()

	for _, v := range []string{
		"WPFLEET_FLEET", "WPFLEET_ENV", "WPFLEET_OUTPUT", "WPFLEET_VERBOSE", "WPFLEET_CONFIG", "WPFLEET_DATA",
		"WPFLEET_S3_ACCESS_KEY_ID", "WPFLEET_SESSION_KEY", "WPFLEET_GIT_TOKEN",
	} {
		testutil.True(t, v+" is reserved", config.ReservedEnv(v))
	}

	for _, v := range []string{"WPFLEET_REGISTRY", "WPFLEET_SERVER__PUBLICURL", "OUTPUT", "HOME"} {
		testutil.True(t, v+" is not reserved", !config.ReservedEnv(v))
	}
}
