// Package golden compares test output against files under testdata.
//
// Regeneration is driven by BALLPOINT_UPDATE_GOLDEN rather than a -update
// flag. `go test ./...` hands every flag to every test binary, so a flag
// registered in one package makes `go test ./... -update` fail in all the
// others. Registering it everywhere would work but leaves a contract each new
// test package has to remember, and nothing would catch a package that
// forgot. An environment variable needs no registration and no contract.
//
// Test-only. Nothing outside _test.go files imports this, so the testing
// dependency never reaches the shipped binary.
package golden

import (
	"os"
	"path/filepath"
	"testing"
)

// updateEnv is the variable that switches Assert from comparing to rewriting.
const updateEnv = "BALLPOINT_UPDATE_GOLDEN"

// Assert compares got against testdata/<name>. Setting BALLPOINT_UPDATE_GOLDEN
// to a non-empty value rewrites that file instead.
func Assert(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if os.Getenv(updateEnv) != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}

	if got != string(want) {
		t.Errorf("output mismatch for %s\n got: %q\nwant: %q\nrerun with %s=1 to accept", name, got, want, updateEnv)
	}
}
