package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/store"
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

// parseProbeFlags parses the probe subcommand's own flags, so the systemd unit
// can pass a secrets path and a concurrency bound.
func TestParseProbeFlags(t *testing.T) {
	var stderr bytes.Buffer
	f, helped, err := parseProbeFlags([]string{"--dry-run", "--secrets-path", "/tmp/s.json", "--concurrency", "4"}, &stderr)
	if err != nil {
		t.Fatalf("parseProbeFlags error = %v", err)
	}
	if helped {
		t.Fatal("parseProbeFlags reported help for a normal parse")
	}
	if !f.dryRun || f.secretsPath != "/tmp/s.json" || f.concurrency != 4 {
		t.Errorf("flags = %+v, want dryRun, /tmp/s.json, 4", f)
	}
}

// The defaults leave the secrets path empty (the binary default applies) and
// concurrency zero (the Todoist client default of 12 applies).
func TestParseProbeFlagsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	f, _, err := parseProbeFlags(nil, &stderr)
	if err != nil {
		t.Fatalf("parseProbeFlags error = %v", err)
	}
	if f.secretsPath != "" || f.concurrency != 0 {
		t.Errorf("defaults = %+v, want empty path and 0 concurrency", f)
	}
}

func TestParseProbeFlagsRejectsPositional(t *testing.T) {
	var stderr bytes.Buffer
	if _, _, err := parseProbeFlags([]string{"extra"}, &stderr); err == nil {
		t.Error("parseProbeFlags accepted a positional argument, want an error")
	}
}

// An empty --secrets-path resolves to the off-store default; a set path is used
// verbatim.
func TestSecretsPathOrDefault(t *testing.T) {
	if got, _ := secretsPathOrDefault("/x/y.json"); got != "/x/y.json" {
		t.Errorf("secretsPathOrDefault(set) = %q, want the given path", got)
	}
	got, err := secretsPathOrDefault("")
	if err != nil {
		t.Fatalf("secretsPathOrDefault(empty) error = %v", err)
	}
	if got == "" || !strings.HasSuffix(got, "nixos-secrets/secrets.json") {
		t.Errorf("secretsPathOrDefault(empty) = %q, want the off-store default", got)
	}
}

// A real (non-dry-run) probe persists the task corpus and the freshness report
// to the cache, so the TUI (issue #5) can walk them offline.
func TestRunProbePersistsCorpus(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	tasks := []sources.Task{{ID: "1", Title: "one"}, {ID: "2", Title: "two"}}
	if err := runProbe(probeDeps{tasks: tasks, stateDir: dir}, &stdout, &stderr); err != nil {
		t.Fatalf("runProbe() error = %v", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadAllTasks()
	if err != nil || len(got) != 2 {
		t.Fatalf("LoadAllTasks() = %d tasks, err=%v, want 2", len(got), err)
	}
	if _, ok, _ := st.LoadReport(); !ok {
		t.Error("probe did not persist a report")
	}
}

// probe --dry-run extracts and groups links from tasks and reports planned
// per-system call counts without touching the network or writing a watermark,
// so it runs green with no credentials. It exercises runProbe with an injected
// task source. Two slack threads in one channel collapse to one planned call.
func TestRunProbeDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	tasks := []sources.Task{{
		ID:    "1",
		Title: "x https://kong.slack.com/archives/C1/p1699999999000100 and https://kong.slack.com/archives/C1/p1699999999000200",
	}}

	err := runProbe(probeDeps{
		tasks:    tasks,
		dryRun:   true,
		stateDir: t.TempDir(),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runProbe() error = %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "slack: 2 links, ~1 calls") {
		t.Errorf("dry-run output = %q, want the two slack links collapsed to one call", got)
	}
}

// probe --help prints the probe FlagSet usage and returns nil. The per-verb
// FlagSet made this a real help request rather than a stray argument, so pin
// the behaviour.
func TestRunProbeHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"probe", "--help"}, &stdout, &stderr)

	if err != nil {
		t.Errorf("Run(probe --help) error = %v, want nil", err)
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
