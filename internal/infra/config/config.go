// Package config loads wpfleet.yaml with koanf: the defaults, then the file,
// then environment variables with the prefix WPFLEET_ and __ between levels.
// Flags are applied by the commands on top of what Load returns.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"

	"github.com/rahacloud/wpfleet/internal/domain/fleet"
)

// FileName is the configuration file at the root of a fleet.
const FileName = "wpfleet.yaml"

// EnvPrefix is the prefix of the environment variables that override the file.
const EnvPrefix = "WPFLEET_"

// envLevel separates levels in an environment variable name:
// WPFLEET_BACKUP__S3__BUCKET sets backup.s3.bucket.
const envLevel = "__"

// reservedEnv reports whether a WPFLEET_ variable, prefix removed, belongs to
// a flag or holds a secret, and so is never a configuration key.
func reservedEnv(name string) bool {
	switch name {
	case "FLEET", "ENV", "OUTPUT", "VERBOSE", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY",
		"OIDC_CLIENT_SECRET", "SESSION_KEY", "GIT_SSH_KEY", "GIT_TOKEN", "GIT_WEBHOOK_SECRET":
		return true
	default:
		return false
	}
}

// ErrInvalid is returned when the configuration breaks a rule.
var ErrInvalid = errors.New("invalid configuration")

// Config is wpfleet.yaml.
type Config struct {
	// Chart is a chart directory or archive; empty uses the embedded chart.
	Chart string `json:"chart" koanf:"chart"`
	// Registry is where site images are pushed, as <registry>/<site>:<tag>.
	Registry string `json:"registry" koanf:"registry"`
	// Build configures image builds.
	Build Build `json:"build" koanf:"build"`
	// WPCLI says where to get wp-cli.phar.
	WPCLI WPCLI `json:"wpcli" koanf:"wpcli"`
	// Environments are the fleet's environments, in order.
	Environments []Environment `json:"environments" koanf:"environments"`
	// Backup configures where the backups are.
	Backup Backup `json:"backup" koanf:"backup"`
	// Server configures wpfleet serve; the CLI ignores it.
	Server Server `json:"server" koanf:"server"`
}

// Build configures image builds.
type Build struct {
	// Base is the image every site image is built on.
	Base string `json:"base" koanf:"base"`
}

// WPCLI says where to get wp-cli.phar.
type WPCLI struct {
	// URL is the phar's download URL.
	URL string `json:"url" koanf:"url"`
	// SHA512 is the phar's hex SHA-512; the phar is refused when it does
	// not match. The default is the published sum of the default URL's phar.
	SHA512 string `json:"sha512" koanf:"sha512"`
	// AllowUnverified lets an empty SHA512 run the phar unchecked.
	AllowUnverified bool `json:"allowUnverified" koanf:"allowUnverified"`
}

// DefaultWPCLIURL is the wp-cli release wpfleet uses by default.
const DefaultWPCLIURL = "https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar"

// DefaultWPCLISHA512 is the SHA-512 that wp-cli publishes for the phar at
// DefaultWPCLIURL (the .sha512 file of the v2.12.0 release).
const DefaultWPCLISHA512 = "be928f6b8ca1e8dfb9d2f4b75a13aa4aee0896f8a9a0a1c45cd5d2c98605e617" +
	"2e6d014dda2e27f88c98befc16c040cbb2bd1bfa121510ea5cdf5f6a30fe8832"

// Environment is one entry of environments.
type Environment struct {
	// Name is the environment's name.
	Name string `json:"name" koanf:"name"`
	// Namespace is the Kubernetes namespace; required.
	Namespace string `json:"namespace" koanf:"namespace"`
	// Kubeconfig is a kubeconfig file; empty follows the usual KUBECONFIG
	// rules. Relative to the fleet directory.
	Kubeconfig string `json:"kubeconfig" koanf:"kubeconfig"`
	// Context is a kubeconfig context; empty uses the current one.
	Context string `json:"context" koanf:"context"`
	// Values is the values directory, relative to the fleet directory;
	// default environments/<name>.
	Values string `json:"values" koanf:"values"`
	// PromoteFrom is the environment images are promoted from.
	PromoteFrom string `json:"promoteFrom" koanf:"promoteFrom"`
}

// Backup configures where the backups are.
type Backup struct {
	// S3 is the bucket the backup CronJobs write to.
	S3 S3 `json:"s3" koanf:"s3"`
}

// S3 is an S3-compatible bucket. The keys never come from the file.
type S3 struct {
	// Endpoint is the service's URL.
	Endpoint string `json:"endpoint" koanf:"endpoint"`
	// Bucket is the bucket's name.
	Bucket string `json:"bucket" koanf:"bucket"`
	// Region is the bucket's region; often empty.
	Region string `json:"region" koanf:"region"`
}

// Default is the configuration before the file and the environment apply.
func Default() Config {
	return Config{
		Chart:        "",
		Registry:     "",
		Build:        Build{Base: "docker.io/library/wordpress:7.0.2-php8.4-apache"},
		WPCLI:        WPCLI{URL: DefaultWPCLIURL, SHA512: DefaultWPCLISHA512, AllowUnverified: false},
		Environments: nil,
		Backup:       Backup{S3: S3{Endpoint: "", Bucket: "", Region: ""}},
		Server:       DefaultServer(),
	}
}

// Load reads <dir>/wpfleet.yaml over the defaults, then applies the WPFLEET_
// environment variables from environ (os.Environ when nil), and validates the
// result.
func Load(dir string, environ func() []string) (Config, error) {
	path := filepath.Join(dir, FileName)

	cfg, err := LoadFile(path, environ)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("%s not found: is %s a fleet directory? (--fleet): %w", path, dir, err)
	}

	return cfg, err
}

// LoadFile is Load for a configuration file anywhere, such as the one a
// ConfigMap mounts for wpfleet serve.
func LoadFile(path string, environ func() []string) (Config, error) {
	k := koanf.New(".")

	err := k.Load(structs.Provider(Default(), "koanf"), nil)
	if err != nil {
		return Config{}, fmt.Errorf("loading defaults: %w", err)
	}

	err = k.Load(file.Provider(path), yaml.Parser())
	if err != nil {
		return Config{}, fmt.Errorf("loading %s: %w", path, err)
	}

	if environ == nil {
		environ = os.Environ
	}

	err = k.Load(env.Provider(".", env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: transformEnv(canonicalKeys(k)),
		EnvironFunc:   environ,
	}), nil)
	if err != nil {
		return Config{}, fmt.Errorf("loading environment: %w", err)
	}

	var cfg Config
	err = k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "koanf", FlatPaths: false, DecoderConfig: nil})
	if err != nil {
		return Config{}, fmt.Errorf("decoding %s: %w", path, err)
	}

	err = cfg.Validate()
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}

	return cfg, nil
}

// canonicalKeys maps every key k holds, in lower case, to its spelling in
// the file and the defaults, so WPFLEET_SERVER__PUBLICURL can set
// server.publicURL: an environment variable has no case to keep.
func canonicalKeys(k *koanf.Koanf) map[string]string {
	keys := k.Keys()
	canon := make(map[string]string, len(keys))

	for _, key := range keys {
		canon[strings.ToLower(key)] = key
	}

	return canon
}

// transformEnv maps WPFLEET_BACKUP__S3__BUCKET to backup.s3.bucket, spelled
// as canon has it, and drops the variables that are not configuration. The
// any is koanf's signature.
func transformEnv(canon map[string]string) func(key, value string) (string, any) {
	return func(key, value string) (string, any) {
		name := strings.TrimPrefix(key, EnvPrefix)
		if reservedEnv(name) {
			return "", nil
		}

		path := strings.ToLower(strings.ReplaceAll(name, envLevel, "."))
		if known, ok := canon[path]; ok {
			path = known
		}

		return path, value
	}
}

// Validate checks that environment names are unique and non-empty, every
// environment has a namespace, every promoteFrom names another existing
// environment, and the server section has no negative duration and no
// access rule that cannot match. ValidateServer checks the rest of the
// server section.
func (c Config) Validate() error {
	var errs []error

	seen := make(map[string]bool, len(c.Environments))

	for i, e := range c.Environments {
		switch {
		case e.Name == "":
			errs = append(errs, fmt.Errorf("%w: environments[%d] has no name", ErrInvalid, i))
		case seen[e.Name]:
			errs = append(errs, fmt.Errorf("%w: environment %q is listed twice", ErrInvalid, e.Name))
		}

		seen[e.Name] = true

		if e.Namespace == "" {
			errs = append(errs, fmt.Errorf("%w: environment %q has no namespace", ErrInvalid, e.Name))
		}
	}

	for _, e := range c.Environments {
		switch {
		case e.PromoteFrom == "":
		case e.PromoteFrom == e.Name:
			errs = append(errs, fmt.Errorf("%w: environment %q promotes from itself", ErrInvalid, e.Name))
		case !seen[e.PromoteFrom]:
			errs = append(errs, fmt.Errorf("%w: environment %q promotes from unknown %q", ErrInvalid, e.Name, e.PromoteFrom))
		}
	}

	errs = append(errs, c.Server.validate(seen)...)

	return errors.Join(errs...)
}

// Fleet turns the configuration of the fleet in dir into the domain model,
// resolving the values directories and kubeconfig files against dir.
func (c Config) Fleet(dir string) fleet.Fleet {
	envs := make([]fleet.Environment, 0, len(c.Environments))

	for _, e := range c.Environments {
		values := e.Values
		if values == "" {
			values = filepath.Join("environments", e.Name)
		}

		envs = append(envs, fleet.Environment{
			Name:        e.Name,
			Namespace:   e.Namespace,
			Kubeconfig:  resolve(dir, e.Kubeconfig),
			Context:     e.Context,
			ValuesDir:   resolve(dir, values),
			PromoteFrom: e.PromoteFrom,
		})
	}

	return fleet.Fleet{Root: dir, Registry: c.Registry, Environments: envs}
}

// resolve makes a relative path relative to dir; empty stays empty.
func resolve(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(dir, path)
}
