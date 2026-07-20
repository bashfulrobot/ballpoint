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
	fs.Usage = func() { fmt.Fprint(stderr, usage) }

	showVersion := fs.Bool("version", false, "print the build version and exit")

	if err := fs.Parse(args); err != nil {
		// fs.Usage has already written the usage text for --help, and asking
		// for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.String())
		return nil
	}

	switch cmd := fs.Arg(0); cmd {
	case "":
		return fmt.Errorf("triage walk: %w", ErrNotImplemented)
	case "probe":
		return fmt.Errorf("probe: %w", ErrNotImplemented)
	case "dispatch":
		return fmt.Errorf("dispatch: %w", ErrNotImplemented)
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}
