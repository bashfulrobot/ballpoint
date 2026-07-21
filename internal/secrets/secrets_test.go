package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecrets(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":"test-token","other":"x"}`)

	got, err := Load(path, "todoist_token")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != "test-token" {
		t.Errorf("Load() = %q, want %q", got, "test-token")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"), "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want a missing-file error")
	}
	if !strings.Contains(err.Error(), "secrets file") {
		t.Errorf("Load() error = %q, want it to mention the secrets file", err)
	}
}

func TestLoadMissingKey(t *testing.T) {
	path := writeSecrets(t, `{"other":"x"}`)

	_, err := Load(path, "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want a missing-key error")
	}
	if !strings.Contains(err.Error(), "todoist_token") {
		t.Errorf("Load() error = %q, want it to name the missing key", err)
	}
}

// A present but empty value is treated as missing, matching the aha script's
// `// empty` jq guard.
func TestLoadEmptyValue(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":""}`)

	_, err := Load(path, "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want an empty-value error")
	}
}

// The secret value must never appear in an error, even when the caller passes
// a wrong key and the file holds a real-looking token.
func TestLoadNeverLeaksValue(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":"super-secret-value"}`)

	_, err := Load(path, "absent_key")
	if err != nil && strings.Contains(err.Error(), "super-secret-value") {
		t.Errorf("Load() error leaked the secret value: %q", err)
	}
}

// A token with a control character is rejected at load, so it never reaches an
// HTTP header, and the value does not appear in the error.
func TestLoadRejectsControlCharacter(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":"abc\ndef"}`)

	_, err := Load(path, "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want a control-character rejection")
	}
	if strings.Contains(err.Error(), "abc") || strings.Contains(err.Error(), "def") {
		t.Errorf("Load() error leaked the token value: %q", err)
	}
}
