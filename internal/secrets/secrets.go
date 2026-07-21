// Package secrets reads values from the off-store secrets file at runtime.
//
// The token cannot come from an environment variable or the Nix store: this
// binary runs under a systemd user timer (issue #4), and user services do not
// inherit session variables. The reference is
// modules/apps/cli/aha-fr-report/default.nix in the nixerator repo, which
// reads its token from the same file inside the script for the same reason.
package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPath returns the standard off-store secrets file,
// ~/.config/nixos-secrets/secrets.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "nixos-secrets", "secrets.json"), nil
}

// Load reads a flat top-level string key from the JSON secrets file at path.
// It returns a distinct error for a missing file and for a missing or empty
// key. The value is returned to the caller and never logged; no error message
// includes it.
func Load(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading secrets file %s: %w", path, err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parsing secrets file %s: %w", path, err)
	}

	raw, ok := doc[key]
	if !ok {
		return "", fmt.Errorf("secrets file %s has no key %q", path, key)
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("key %q in %s is not a string", key, path)
	}

	if value == "" {
		return "", fmt.Errorf("key %q in %s is empty", key, path)
	}

	return value, nil
}
