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
  ballpoint                     walk the triage queue
  ballpoint probe [--benchmark] refresh freshness data
  ballpoint dispatch            run queued work

Flags:
  --version   print the build version and exit
  --help      print this message and exit
`

// displayName labels a verb in error messages. The bare walk has no verb, so
// it reads as "the triage walk" rather than an empty string.
func displayName(cmd string) string {
	if cmd == "" {
		return "the triage walk"
	}

	return cmd
}

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

	cmd := fs.Arg(0)

	// Each verb owns its argument handling. flag stops parsing at the first
	// positional, so a flag written after the verb never reaches the top-level
	// FlagSet. The verbs that take no flags reject any trailing token so a typo
	// in issue #4's systemd unit fails loudly. probe is the first verb that
	// carries a flag, so it parses its own FlagSet.
	switch cmd {
	case "":
		if len(rest) > 1 {
			return fmt.Errorf("%s takes no arguments, got %q", displayName(cmd), rest[1:])
		}

		return fmt.Errorf("triage walk: %w", ErrNotImplemented)
	case "probe":
		pf := flag.NewFlagSet("probe", flag.ContinueOnError)
		pf.SetOutput(stderr)
		// Registered so the documented live command parses as a real flag.
		// Issue #3 wires the prefetch behind it.
		pf.Bool("benchmark", false, "time a full prefetch against the live API and print the wall clock")

		if err := pf.Parse(rest[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}

			return err
		}

		if pf.NArg() > 0 {
			return fmt.Errorf("probe takes no positional arguments, got %q", pf.Args())
		}

		return fmt.Errorf("probe: %w", ErrNotImplemented)
	case "dispatch":
		if len(rest) > 1 {
			return fmt.Errorf("dispatch takes no arguments, got %q", rest[1:])
		}

		return fmt.Errorf("dispatch: %w", ErrNotImplemented)
	default:
		// A failed usage write is deliberately not allowed to mask the real
		// error.
		_, _ = fmt.Fprint(stderr, usage)

		return fmt.Errorf("unknown command %q", cmd)
	}
}
