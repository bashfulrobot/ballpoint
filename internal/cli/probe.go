package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/config"
	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/probe/gwsauth"
	"github.com/bashfulrobot/ballpoint/internal/probe/probeset"
	"github.com/bashfulrobot/ballpoint/internal/probe/salesforce"
	"github.com/bashfulrobot/ballpoint/internal/secrets"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/sources/todoist"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// probeDeps are the inputs to runProbe, injected so tests can supply tasks and
// a temp state dir without a network or a real secrets file.
type probeDeps struct {
	tasks     []sources.Task
	creds     probeset.Credentials
	stateDir  string
	dryRun    bool
	benchmark bool
}

// resolveProbeDeps fills probeDeps from the environment: the state dir, the
// per-source credentials, and the task list from Todoist. Split out so runProbe
// stays testable with injected tasks. A missing per-source credential is not
// fatal; that source renders unchecked. The Todoist token is required because
// it provides the task corpus every run works from.
func resolveProbeDeps(f probeFlags) (probeDeps, error) {
	dir, err := config.StateDir()
	if err != nil {
		return probeDeps{}, err
	}
	deps := probeDeps{stateDir: dir, dryRun: f.dryRun, benchmark: f.benchmark}

	path, err := secretsPathOrDefault(f.secretsPath)
	if err != nil {
		return probeDeps{}, err
	}
	deps.creds.Slack, _ = secrets.Load(path, "slack_token")
	deps.creds.Aha, _ = secrets.Load(path, "aha_token")
	// Google auth lives in the gws CLI's own store, not this secrets file, so
	// there is no google_token key. When gws is present, mint a fresh access
	// token for this run; any failure (gws absent, unauthenticated, or offline)
	// leaves the token empty and renders Gmail and Drive unchecked.
	if gwsauth.Available() {
		if tok, err := gwsauth.New().AccessToken(context.Background()); err == nil {
			deps.creds.Google = tok
		}
	}
	// Salesforce auth lives in the sf CLI's own store, not this secrets file, so
	// the prober is gated on the binary being present rather than a token.
	deps.creds.Salesforce = salesforce.Available()

	token, err := secrets.Load(path, "todoist_token")
	if err != nil {
		return probeDeps{}, fmt.Errorf("loading todoist token: %w", err)
	}
	delta, err := todoist.New(token, todoist.WithConcurrency(f.concurrency)).Probe(context.Background(), sources.Watermark{})
	if err != nil {
		return probeDeps{}, fmt.Errorf("fetching tasks: %w", err)
	}
	deps.tasks = delta.Tasks
	return deps, nil
}

// secretsPathOrDefault returns the explicit path when set, otherwise the
// off-store default. A flag override lets a systemd unit point at a per-host
// secrets file without an environment variable.
func secretsPathOrDefault(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return secrets.DefaultPath()
}

// runProbe executes the probe. In dry-run it prints the planned per-system call
// counts and makes no prober call and writes no watermark. Otherwise it runs
// the engine, persists the next watermark, and writes the JSON report to
// stdout.
func runProbe(deps probeDeps, stdout, stderr io.Writer) error {
	if deps.dryRun {
		return dryRunPlan(deps.tasks, stdout)
	}

	st, err := store.Open(deps.stateDir)
	if err != nil {
		return err
	}
	since, err := st.LoadWatermark()
	if err != nil {
		return err
	}

	// Persist the corpus so the TUI (issue #5) walks it offline. A single task
	// that fails to cache is not fatal; the walk simply skips a missing card.
	keep := make(map[string]bool, len(deps.tasks))
	for _, task := range deps.tasks {
		keep[task.ID] = true
		if err := st.SaveTask(task); err != nil {
			_, _ = fmt.Fprintf(stderr, "warning: caching task %s: %v\n", task.ID, err)
		}
	}
	// Evict tasks completed or deleted in Todoist since the last probe. Without
	// this the cache only grows and the walk keeps presenting done tasks.
	if _, err := st.PruneTasksExcept(keep); err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: pruning stale cache entries: %v\n", err)
	}

	start := time.Now()
	report, next, err := probe.Run(context.Background(), deps.tasks, since, probeset.Build(deps.creds))
	if err != nil {
		return err
	}
	if err := st.SaveWatermark(next); err != nil {
		return err
	}
	// The report is the freshness overlay the TUI reads per card.
	if err := st.SaveReport(report); err != nil {
		return err
	}

	if deps.benchmark {
		// A diagnostic on stderr; a failed write must not mask the report.
		_, _ = fmt.Fprintf(stderr, "probe wall clock: %v over %d tasks\n", time.Since(start), len(deps.tasks))
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// dryRunPlan prints how many calls each system would take, proving the
// batch-by-system collapse without making any prober call. Slack collapses to
// one history call per distinct channel; every other system is one call for the
// whole batch.
func dryRunPlan(tasks []sources.Task, stdout io.Writer) error {
	channels := map[string]bool{}
	records := map[links.System]int{}
	for _, task := range tasks {
		for _, l := range links.Extract(task) {
			records[l.System]++
			if l.System == links.SystemSlack {
				if ch := l.Fields["channel"]; ch != "" {
					channels[ch] = true
				}
			}
		}
	}

	systems := make([]string, 0, len(records))
	for s := range records {
		systems = append(systems, string(s))
	}
	sort.Strings(systems)

	for _, s := range systems {
		sys := links.System(s)
		calls := records[sys]
		if sys == links.SystemSlack {
			// One history call per channel, replies only for advanced threads.
			calls = len(channels)
		}
		if _, err := fmt.Fprintf(stdout, "%s: %d links, ~%d calls\n", s, records[sys], calls); err != nil {
			return err
		}
	}
	return nil
}
