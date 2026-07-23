package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/config"
	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/probe/github"
	"github.com/bashfulrobot/ballpoint/internal/probe/gwsauth"
	"github.com/bashfulrobot/ballpoint/internal/probe/probeset"
	"github.com/bashfulrobot/ballpoint/internal/probe/salesforce"
	"github.com/bashfulrobot/ballpoint/internal/probe/slack"
	"github.com/bashfulrobot/ballpoint/internal/probe/slackauth"
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
func resolveProbeDeps(f probeFlags, stderr io.Writer) (probeDeps, error) {
	dir, err := config.StateDir()
	if err != nil {
		return probeDeps{}, err
	}
	deps := probeDeps{stateDir: dir, dryRun: f.dryRun, benchmark: f.benchmark}

	path, err := secretsPathOrDefault(f.secretsPath)
	if err != nil {
		return probeDeps{}, err
	}
	// Slack auth lives in the slack-token-refresh store, not this secrets file,
	// so there is no slack_token key. slackResolver maps each link's workspace
	// host to its xoxc/xoxd pair; a missing or unreadable store leaves the
	// resolver nil and renders Slack unchecked.
	deps.creds.Slack = slackResolver(stderr)
	deps.creds.Aha, _ = secrets.Load(path, "aha_token")
	// Google auth lives in the gws CLI's own store, not this secrets file, so
	// there is no google_token key. googleToken mints a fresh access token for
	// this run; an absent, unauthenticated, or offline gws leaves it empty and
	// renders Gmail and Drive unchecked.
	deps.creds.Google = googleToken(gwsauth.New(), stderr)
	// Salesforce auth lives in the sf CLI's own store, not this secrets file, so
	// the prober is gated on the binary being present rather than a token.
	deps.creds.Salesforce = salesforce.Available()
	// GitHub auth lives in the gh CLI's own store, not this secrets file, so the
	// prober is gated on the binary being present rather than a token.
	deps.creds.GitHub = github.Available()

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

// slackResolver loads the slack-token-refresh credentials store and returns a
// resolver mapping a link's workspace host to its xoxc/xoxd pair. A missing
// store is the normal state on a host that never ran the refresher and is
// silent; an unreadable or malformed store warns and returns nil. A nil resolver
// leaves the Slack prober unregistered, so Slack links render unchecked without
// failing the run.
func slackResolver(stderr io.Writer) slack.Resolver {
	path, err := slackauth.DefaultPath()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: locating slack credentials failed, Slack unchecked: %v\n", err)
		return nil
	}
	store, err := slackauth.Load(path)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: slack credentials unreadable, Slack unchecked: %v\n", err)
		return nil
	}
	if store.Empty() {
		return nil
	}
	return func(host string) (slack.Creds, bool) {
		c, ok := store.ForHost(host)
		if !ok {
			return slack.Creds{}, false
		}
		return slack.Creds{Token: c.Token, Cookie: c.Cookie}, true
	}
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

// gwsTimeout bounds the whole Google token acquisition (the gws subprocess plus
// the OAuth exchange), so a wedged CLI degrades to unchecked instead of hanging
// the probe indefinitely.
const gwsTimeout = 30 * time.Second

// gwsAvailable is indirected so tests drive the resolution without gws on PATH.
var gwsAvailable = gwsauth.Available

// googleToken mints a Google access token from gws for this run, or "" when gws
// is absent, unauthenticated, or unreachable, in which case Gmail and Drive
// render unchecked. An absent gws stays silent (a common, expected setup); a
// present-but-failing gws (a revoked token, an offline host) writes a non-fatal
// stderr warning, so a user can tell "install gws" from "re-run gws auth login".
func googleToken(src *gwsauth.Source, stderr io.Writer) string {
	if !gwsAvailable() {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), gwsTimeout)
	defer cancel()
	tok, err := src.AccessToken(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: gws Google auth unavailable, Gmail and Drive unchecked: %v\n", err)
		return ""
	}
	return tok
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

// slackChannelHost returns the lowercased host of a Slack permalink for the
// dry-run call estimate, mirroring how the prober groups channels by host. An
// unparseable link contributes an empty host, so it still groups by channel.
func slackChannelHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
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
					// One history call per channel per workspace, matching the
					// prober's host+channel grouping, so a shared channel ID across
					// two workspaces counts as two calls, not one.
					channels[slackChannelHost(l.Raw)+"\x00"+ch] = true
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
