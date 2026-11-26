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
	case "FLEET", "ENV", "OUTPUT", "VERBOSE", "S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY":
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
	// SHA512 is the phar's hex SHA-512; checked when set.
	SHA512 string `json:"sha512" koanf:"sha512"`
}

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
		Chart:    "",
		Registry: "",
		Build:    Build{Base: "docker.io/library/wordpress:7.0.2-php8.4-apache"},
		WPCLI: WPCLI{
			URL:    "https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar",
			SHA512: "",
		},
		Environments: nil,
		Backup:       Backup{S3: S3{Endpoint: "", Bucket: "", Region: ""}},
	}
}

// Load reads <dir>/wpfleet.yaml over the defaults, then applies the WPFLEET_
// environment variables from environ (os.Environ when nil), and validates the
// result.
func Load(dir string, environ func() []string) (Config, error) {
	k := koanf.New(".")

	err := k.Load(structs.Provider(Default(), "koanf"), nil)
	if err != nil {
		return Config{}, fmt.Errorf("loading defaults: %w", err)
	}

	path := filepath.Join(dir, FileName)
	err = k.Load(file.Provider(path), yaml.Parser())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%s not found: is %s a fleet directory? (--fleet): %w", path, dir, err)
		}

		return Config{}, fmt.Errorf("loading %s: %w", path, err)
	}

	if environ == nil {
		environ = os.Environ
	}

	err = k.Load(env.Provider(".", env.Opt{
		Prefix:        EnvPrefix,
		TransformFunc: transformEnv,
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

// transformEnv maps WPFLEET_BACKUP__S3__BUCKET to backup.s3.bucket and drops
// the variables that are not configuration. The any is koanf's signature.
func transformEnv(key, value string) (string, any) {
	name := strings.TrimPrefix(key, EnvPrefix)
	if reservedEnv(name) {
		return "", nil
	}

	return strings.ToLower(strings.ReplaceAll(name, envLevel, ".")), value
}

// Validate checks that environment names are unique and non-empty, every
// environment has a namespace, and every promoteFrom names another existing
// environment.
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
