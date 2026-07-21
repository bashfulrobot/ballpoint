package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/config"
	"github.com/bashfulrobot/ballpoint/internal/dispatch"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/store"
	"github.com/bashfulrobot/ballpoint/internal/tui"
)

// dispatchFlags are the dispatch subcommand's own flags.
type dispatchFlags struct {
	concurrency int
	model       string
	scriptsDir  string
	dryRun      bool
	status      bool
}

// parseDispatchFlags parses the dispatch FlagSet. helped is true when --help
// was asked, which flag already wrote.
func parseDispatchFlags(args []string, stderr io.Writer) (flags dispatchFlags, helped bool, err error) {
	df := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	df.SetOutput(stderr)
	df.IntVar(&flags.concurrency, "concurrency", 2, "max concurrent jobs (conservative; every worker shares the same quota)")
	df.StringVar(&flags.model, "model", "haiku", "claude model alias or id for the jobs")
	df.StringVar(&flags.scriptsDir, "scripts-dir", "", "override the triage macro scripts directory")
	df.BoolVar(&flags.dryRun, "dry-run", false, "print each prompt and planned write, invoke nothing")
	df.BoolVar(&flags.status, "status", false, "print job status for the current dispatch state and exit")

	if err := df.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return dispatchFlags{}, true, nil
		}
		return dispatchFlags{}, false, err
	}
	if df.NArg() > 0 {
		return dispatchFlags{}, false, fmt.Errorf("dispatch takes no positional arguments, got %q", df.Args())
	}
	return flags, false, nil
}

// dispatchDeps is the resolved environment the run needs.
type dispatchDeps struct {
	store       *store.Store
	root        string
	report      probe.Report
	entries     []queue.Entry
	scriptsDir  string
	model       string
	concurrency int
	dryRun      bool
	statusOnly  bool
}

// resolveDispatchDeps fills the environment: state root, store, freshness
// report, queue snapshot, and scripts directory.
func resolveDispatchDeps(f dispatchFlags) (dispatchDeps, error) {
	root, err := config.StateDir()
	if err != nil {
		return dispatchDeps{}, err
	}
	st, err := store.Open(root)
	if err != nil {
		return dispatchDeps{}, err
	}
	report, _, err := st.LoadReport()
	if err != nil {
		return dispatchDeps{}, err
	}
	entries, err := queue.Load(root)
	if err != nil {
		return dispatchDeps{}, err
	}
	scriptsDir := f.scriptsDir
	if scriptsDir == "" {
		scriptsDir, err = tui.DefaultScriptsDir()
		if err != nil {
			return dispatchDeps{}, err
		}
	}
	return dispatchDeps{
		store:       st,
		root:        root,
		report:      report,
		entries:     entries,
		scriptsDir:  scriptsDir,
		model:       f.model,
		concurrency: f.concurrency,
		dryRun:      f.dryRun,
		statusOnly:  f.status,
	}, nil
}

func nowUTC() time.Time { return time.Now().UTC() }

// runDispatch runs the dispatcher or, with --status, prints the last run's job
// statuses.
func runDispatch(deps dispatchDeps, stdout, _ io.Writer) error {
	if deps.statusOnly {
		statuses, err := dispatch.LoadStatuses(deps.root)
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			_, _ = fmt.Fprintln(stdout, "no dispatch jobs")
			return nil
		}
		for _, s := range statuses {
			line := fmt.Sprintf("%s\t%s", s.TaskID, s.State)
			if s.Detail != "" {
				line += "\t" + s.Detail
			}
			_, _ = fmt.Fprintln(stdout, line)
		}
		return nil
	}

	cfg := dispatch.Config{
		Store:       deps.store,
		Root:        deps.root,
		Report:      deps.report,
		Entries:     deps.entries,
		ScriptsDir:  deps.scriptsDir,
		Concurrency: deps.concurrency,
		DryRun:      deps.dryRun,
		Now:         nowUTC,
		Assess:      dispatch.ExecAssess(deps.model),
		RunScript:   dispatch.ExecScript,
		Stdout:      stdout,
	}
	sum, err := dispatch.Run(context.Background(), cfg)
	if err != nil {
		return err
	}
	if deps.dryRun {
		_, _ = fmt.Fprintf(stdout, "dry run: %d task(s) would be dispatched\n", sum.Skipped)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "dispatched: %d succeeded, %d failed, %d requeued\n", sum.Succeeded, sum.Failed, sum.Requeued)
	return nil
}
