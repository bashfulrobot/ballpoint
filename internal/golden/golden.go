// Package golden compares test output against files under testdata.
//
// The -update flag lives here rather than in one test package because
// `go test ./...` passes every flag to every test binary. A flag registered
// in a single package makes `go test ./... -update` fail in all the others.
// Any test package that links this one therefore accepts -update, which is
// why packages with no golden files of their own still blank import it.
//
// Test-only. Nothing in cmd/ or the rest of internal/ imports this, so the
// testing dependency never reaches the shipped binary.
package golden

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files with current output")

// Assert compares got against testdata/<name>, rewriting that file when the
// suite runs with -update.
func Assert(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // a checked-in fixture is world readable by design
			t.Fatalf("writing golden %s: %v", path, err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}

	if got != string(want) {
		t.Errorf("output mismatch for %s\n got: %q\nwant: %q\nrerun with -update to accept", name, got, want)
	}
}
