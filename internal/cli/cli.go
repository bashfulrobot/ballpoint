// Package cli implements ballpoint's command line surface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
)

// ErrNotImplemented reports a subcommand that is wired but carries no
// behaviour yet. Commands wrap it so main exits non-zero rather than letting
// a systemd timer record success for work that never ran.
var ErrNotImplemented = errors.New("not implemented")

const usage = `ballpoint drives Todoist triage.

Usage:
  ballpoint            walk the triage queue
  ballpoint probe      refresh freshness data
  ballpoint dispatch   run queued work

Flags:
  --version   print the build version and exit
  --help      print this message and exit
`

// Run executes the command named by args, which excludes the program name.
// Normal output goes to stdout and diagnostics to stderr so callers, and
// tests, can capture them independently.
func Run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ballpoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	// flag's Usage callback cannot report an error, and a caller that loses
	// this diagnostic still gets the returned error, so the write is
	// deliberately unchecked.
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }

	showVersion := fs.Bool("version", false, "print the build version and exit")

	if err := fs.Parse(args); err != nil {
		// fs.Usage has already written the usage text for --help, and asking
		// for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	rest := fs.Args()

	if *showVersion {
		if len(rest) > 0 {
			return fmt.Errorf("--version takes no arguments, got %q", rest)
		}

		// This is the command's actual output rather than a diagnostic, so a
		// failed write is a failure of the command.
		if _, err := fmt.Fprintln(stdout, buildinfo.String()); err != nil {
			return fmt.Errorf("writing version: %w", err)
		}

		return nil
	}

	switch cmd := fs.Arg(0); cmd {
	case "":
		return fmt.Errorf("triage walk: %w", ErrNotImplemented)
	case "probe", "dispatch":
		// flag stops parsing at the first positional, so a flag written after
		// the subcommand never reaches this FlagSet and would otherwise be
		// dropped in silence. Reject the extra arguments instead. This lives
		// inside the known-command case so a mistyped verb still gets told it
		// is unknown. Per-verb FlagSets arrive with the first verb that
		// actually takes flags.
		if len(rest) > 1 {
			return fmt.Errorf("%s takes no arguments, got %q", cmd, rest[1:])
		}

		return fmt.Errorf("%s: %w", cmd, ErrNotImplemented)
	default:
		// The unknown command is the error worth reporting, so a failed
		// usage write is deliberately not allowed to mask it.
		_, _ = fmt.Fprint(stderr, usage)

		return fmt.Errorf("unknown command %q", cmd)
	}
}
