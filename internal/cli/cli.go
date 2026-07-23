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
  ballpoint [--project P | --filter Q | --preset P | --task ID] [--refresh]
                                         walk the triage queue (bare, with no scope flag, opens a picker)
  ballpoint probe [--dry-run] [--benchmark] [--secrets-path PATH] [--concurrency N]
                                         refresh freshness data
  ballpoint dispatch [--concurrency N] [--model M] [--scripts-dir D] [--dry-run] [--status]
                                         assess queued tasks and write work-log entries

Flags:
  --project P      walk one project
  --filter Q       walk a filter query against the cache: @label #project p1..p4,
                   combined with & | ! and parens; other terms match as substrings
  --preset P       walk a named preset (same query syntax as --filter)
  --task ID        walk a single task
  --scripts-dir D  override the triage macro scripts directory
  --refresh        run probe before walking to refresh the cache
  --version        print the build version and exit
  --help           print this message and exit
`

// displayName labels a verb in error messages. The bare walk has no verb, so
// it reads as "the triage walk" rather than an empty string.
func displayName(cmd string) string {
	if cmd == "" {
		return "the triage walk"
	}

	return cmd
}

// probeFlags are the parsed flags of the probe subcommand.
type probeFlags struct {
	dryRun      bool
	benchmark   bool
	secretsPath string // empty means the off-store default
	concurrency int    // zero means the Todoist client default
}

// parseProbeFlags parses the probe subcommand's own FlagSet. helped is true when
// the caller asked for --help, which flag has already written, so Run returns
// nil without running the probe.
func parseProbeFlags(args []string, stderr io.Writer) (flags probeFlags, helped bool, err error) {
	pf := flag.NewFlagSet("probe", flag.ContinueOnError)
	pf.SetOutput(stderr)
	pf.BoolVar(&flags.dryRun, "dry-run", false, "report planned per-system calls without probing or writing watermarks")
	pf.BoolVar(&flags.benchmark, "benchmark", false, "time the real pass and print the wall clock")
	pf.StringVar(&flags.secretsPath, "secrets-path", "", "path to the off-store secrets file (default ~/.config/nixos-secrets/secrets.json)")
	pf.IntVar(&flags.concurrency, "concurrency", 0, "bounded Todoist fetch concurrency (default 12)")

	if err := pf.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return probeFlags{}, true, nil
		}
		return probeFlags{}, false, err
	}
	if pf.NArg() > 0 {
		return probeFlags{}, false, fmt.Errorf("probe takes no positional arguments, got %q", pf.Args())
	}
	return flags, false, nil
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

	// The walk is the default command, so its scope flags live on the top-level
	// FlagSet. That lets `ballpoint --project Kong` reach the walk without a verb
	// token; a bare `ballpoint` with no scope flag opens the picker.
	var wf walkFlags
	fs.StringVar(&wf.project, "project", "", "walk one project")
	fs.StringVar(&wf.filter, "filter", "", "walk a filter query: @label #project p1..p4 with & | ! and parens; other terms match as substrings")
	fs.StringVar(&wf.preset, "preset", "", "walk a named preset (same query syntax as --filter)")
	fs.StringVar(&wf.task, "task", "", "walk a single task id")
	fs.StringVar(&wf.scriptsDir, "scripts-dir", "", "override the triage macro scripts directory")
	fs.BoolVar(&wf.refresh, "refresh", false, "run probe before walking to refresh the cache")

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

		return runWalk(wf, stdout, stderr)
	case "probe":
		f, helped, err := parseProbeFlags(rest[1:], stderr)
		if err != nil {
			return err
		}
		if helped {
			return nil
		}

		deps, err := resolveProbeDeps(f, stderr)
		if err != nil {
			return err
		}

		return runProbe(deps, stdout, stderr)
	case "dispatch":
		f, helped, err := parseDispatchFlags(rest[1:], stderr)
		if err != nil {
			return err
		}
		if helped {
			return nil
		}

		deps, err := resolveDispatchDeps(f)
		if err != nil {
			return err
		}

		return runDispatch(deps, stdout, stderr)
	default:
		// A failed usage write is deliberately not allowed to mask the real
		// error.
		_, _ = fmt.Fprint(stderr, usage)

		return fmt.Errorf("unknown command %q", cmd)
	}
}
