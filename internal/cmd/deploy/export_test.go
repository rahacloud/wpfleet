package deploy

import (
	"github.com/urfave/cli/v3"

	"github.com/rahacloud/wpfleet/internal/cmd/cmdutil"
)

// CommandWith builds wpfleet deploy on the Releases that releases gives.
func CommandWith(releases func(*cmdutil.Env) (Releases, error)) *cli.Command {
	return command(releases)
}

// DiffCommandWith builds wpfleet diff on the Releases that releases gives.
func DiffCommandWith(releases func(*cmdutil.Env) (Releases, error)) *cli.Command {
	return diffCommand(releases)
}

// Normalize normalises a manifest the way diff does, with a redactor of its
// own.
func Normalize(manifests ...string) []string {
	r := newRedactor()
	out := make([]string, 0, len(manifests))

	for _, m := range manifests {
		out = append(out, r.normalize(m))
	}

	return out
}
