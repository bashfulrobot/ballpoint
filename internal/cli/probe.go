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
func resolveProbeDeps(dryRun, benchmark bool) (probeDeps, error) {
	dir, err := config.StateDir()
	if err != nil {
		return probeDeps{}, err
	}
	deps := probeDeps{stateDir: dir, dryRun: dryRun, benchmark: benchmark}

	path, err := secrets.DefaultPath()
	if err != nil {
		return probeDeps{}, err
	}
	deps.creds.Slack, _ = secrets.Load(path, "slack_token")
	deps.creds.Aha, _ = secrets.Load(path, "aha_token")
	deps.creds.Google, _ = secrets.Load(path, "google_token")
	// Salesforce auth lives in the sf CLI's own store, not this secrets file, so
	// the prober is gated on the binary being present rather than a token.
	deps.creds.Salesforce = salesforce.Available()

	token, err := secrets.Load(path, "todoist_token")
	if err != nil {
		return probeDeps{}, fmt.Errorf("loading todoist token: %w", err)
	}
	delta, err := todoist.New(token).Probe(context.Background(), sources.Watermark{})
	if err != nil {
		return probeDeps{}, fmt.Errorf("fetching tasks: %w", err)
	}
	deps.tasks = delta.Tasks
	return deps, nil
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

	start := time.Now()
	report, next, err := probe.Run(context.Background(), deps.tasks, since, probeset.Build(deps.creds))
	if err != nil {
		return err
	}
	if err := st.SaveWatermark(next); err != nil {
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
