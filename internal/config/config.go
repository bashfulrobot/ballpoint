// Package config resolves ballpoint's on-disk locations.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// appDir is ballpoint's directory name under the XDG base directories.
const appDir = "ballpoint"

// StateDir returns the directory holding ballpoint's cache and watermarks.
// It honours XDG_STATE_HOME when that names an absolute path and otherwise
// falls back to the specification default of ~/.local/state.
//
// A relative XDG_STATE_HOME is ignored rather than resolved against the
// working directory. The XDG specification requires absolute paths, and
// ballpoint runs under a systemd timer where the working directory carries no
// meaning.
func StateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, appDir), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}

	return filepath.Join(home, ".local", "state", appDir), nil
}
