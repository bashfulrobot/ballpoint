package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
	"github.com/bashfulrobot/ballpoint/internal/golden"
)

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

// flag stops parsing at the first positional, so anything after a subcommand
// is invisible to the FlagSet. A typo'd flag in issue #4's systemd unit must
// fail loudly rather than silently running the verb with defaults.
func TestRunRejectsStrayArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "flag after a subcommand", args: []string{"probe", "--nonexistent"}},
		{name: "argument after a subcommand", args: []string{"dispatch", "extra"}},
		{name: "argument after --version", args: []string{"--version", "dispatch"}},
		// The bare walk has an empty first argument, so a trailing token must
		// not slip past the stray-argument guard.
		{name: "argument after an explicit empty verb", args: []string{"", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := Run(tt.args, &stdout, &stderr)

			if err == nil {
				t.Fatalf("Run(%q) error = nil, want a stray-argument error", tt.args)
			}

			if errors.Is(err, ErrNotImplemented) {
				t.Errorf("Run(%q) reported ErrNotImplemented instead of rejecting the stray argument", tt.args)
			}

			if stdout.Len() != 0 {
				t.Errorf("Run(%q) wrote %q to stdout, want nothing", tt.args, stdout.String())
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare unknown verb", args: []string{"nope"}},
		// A mistyped verb with trailing arguments must still be reported as
		// unknown, not as a verb that takes no arguments.
		{name: "unknown verb with trailing arguments", args: []string{"nope", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := Run(tt.args, &stdout, &stderr)

			if err == nil {
				t.Fatalf("Run(%q) error = nil, want an unknown-command error", tt.args)
			}

			if errors.Is(err, ErrNotImplemented) {
				t.Errorf("Run(%q) reported ErrNotImplemented for an unknown command", tt.args)
			}

			if got := err.Error(); !strings.Contains(got, "unknown command") {
				t.Errorf("Run(%q) error = %q, want it to name the command as unknown", tt.args, got)
			}
		})
	}
}

// probe --benchmark is still not implemented, but it must parse as a known
// flag rather than an unknown-argument error, so the documented live command
// is real.
func TestRunProbeBenchmarkParses(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"probe", "--benchmark"}, &stdout, &stderr)

	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Run(probe --benchmark) error = %v, want ErrNotImplemented", err)
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

	golden.Assert(t, "usage.golden", stderr.String())
}
