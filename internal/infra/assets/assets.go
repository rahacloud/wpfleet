// Package assets holds the files wpfleet embeds to run inside a site's pod:
// the PHP that copy runs with wp eval-file after search-replace.
package assets

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// RehostExtraName is the file name of the page builder fix-up.
const RehostExtraName = "rehost-extra.php"

// RehostExtra is the part of a host change that wp-cli search-replace cannot
// do: it rewrites BeTheme's base64-encoded page builder data and deletes
// WPML's cached switcher templates. Run it as
// wp eval-file rehost-extra.php <old-host> <new-host> [dry-run].
//
//go:embed rehost-extra.php
var RehostExtra []byte

// fileMode is the mode of a file WriteTemp writes; CopyFile keeps it in the
// pod, where PHP only needs to read it.
const fileMode = 0o644

// WriteTemp writes an embedded file into a new temporary directory, so that
// it can be copied into a pod, and returns its path and a function that
// removes it.
func WriteTemp(name string, data []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "wpfleet-assets-")
	if err != nil {
		return "", nil, fmt.Errorf("writing %s: %w", name, err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, name)

	err = os.WriteFile(path, data, fileMode)
	if err != nil {
		cleanup()

		return "", nil, fmt.Errorf("writing %s: %w", name, err)
	}

	return path, cleanup, nil
}
