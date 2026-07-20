package cli

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
)

var update = flag.Bool("update", false, "rewrite golden files with current output")

// assertGolden compares got against testdata/<name>, rewriting it when the
// suite runs with -update. Later issues follow this convention.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}

	if got != string(want) {
		t.Errorf("output mismatch for %s\n got: %q\nwant: %q", name, got, want)
	}
}

// Every wired but unbuilt subcommand must report ErrNotImplemented so main
// exits non-zero. A systemd timer must not record success for work that never
// ran.
func TestRunNotImplemented(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare invocation is the triage walk", args: []string{}},
		{name: "probe", args: []string{"probe"}},
		{name: "dispatch", args: []string{"dispatch"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := Run(tt.args, &stdout, &stderr)

			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("Run(%q) error = %v, want ErrNotImplemented", tt.args, err)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"nope"}, &stdout, &stderr)

	if err == nil {
		t.Fatal("Run() error = nil, want an unknown-command error")
	}

	if errors.Is(err, ErrNotImplemented) {
		t.Error("Run() reported ErrNotImplemented for an unknown command")
	}
}

func TestRunVersion(t *testing.T) {
	original := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = original })

	buildinfo.Version = "1.2.3"

	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := stdout.String(), "1.2.3\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// errWriter fails every write, standing in for a closed pipe.
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

// Losing --version output to a closed stdout must surface as an error rather
// than a silent success.
func TestRunVersionWriteFailure(t *testing.T) {
	sentinel := errors.New("stdout closed")

	var stderr bytes.Buffer

	err := Run([]string{"--version"}, errWriter{err: sentinel}, &stderr)

	if !errors.Is(err, sentinel) {
		t.Errorf("Run() error = %v, want it to wrap %v", err, sentinel)
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v, want nil for --help", err)
	}

	assertGolden(t, "usage.golden", stderr.String())
}
