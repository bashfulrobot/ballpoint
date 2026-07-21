package probe

import (
	"context"
	"sort"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// runDeadline bounds one whole Run, the same way #2's Probe does.
const runDeadline = 5 * time.Minute

// maxLinksPerSystem caps how many distinct records one system is probed for in a
// single run. Task text is attacker-influenceable, and the REST probers issue
// one request per record, so without a cap a single comment stuffed with
// thousands of distinct ids could amplify into thousands of outbound requests,
// burn the account's API quota, and starve every other task's links inside the
// run deadline. The cap sits far above any real corpus (the measured one is 148
// links across all systems); the excess renders unchecked, never dropped.
const maxLinksPerSystem = 500

// LinkFreshness is the engine's per-link verdict in the report.
type LinkFreshness struct {
	Key          string     `json:"key"`
	System       string     `json:"system"`
	Raw          string     `json:"raw"`
	LastActivity *time.Time `json:"last_activity,omitempty"`
	Changed      bool       `json:"changed,omitempty"`
	Unchecked    bool       `json:"unchecked,omitempty"`
	Reason       Reason     `json:"reason,omitempty"`
}

// TaskReport is one task's freshness.
type TaskReport struct {
	Title      string          `json:"title"`
	HasWorkLog bool            `json:"has_work_log"`
	LastLogged *time.Time      `json:"last_logged,omitempty"`
	Links      []LinkFreshness `json:"links"`
}

// Report is the whole run, keyed by task id.
type Report struct {
	Tasks map[string]TaskReport `json:"tasks"`
}

// reasonForUnprobed maps a system with no registered prober to its unchecked
// reason. Teams is explicitly not probeable; everything else is a missing
// prober.
func reasonForUnprobed(s links.System) Reason {
	if s == links.SystemTeams {
		return ReasonNotProbeable
	}
	return ReasonNoProbe
}

// Run extracts each task's links, probes them batched by system, reconciles
// against each task's work log, and returns the report plus the next
// watermark. Only checked links contribute a watermark, so an unchecked link
// never advances one.
func Run(ctx context.Context, tasks []sources.Task, since sources.Watermark, reg *Registry) (Report, sources.Watermark, error) {
	ctx, cancel := context.WithTimeout(ctx, runDeadline)
	defer cancel()

	// Extract once, remember each task's links.
	taskLinks := make(map[string][]links.Link, len(tasks))
	bySystem := map[links.System][]links.Link{}
	seen := map[string]bool{}
	for _, task := range tasks {
		ls := links.Extract(task)
		taskLinks[task.ID] = ls
		for _, l := range ls {
			if seen[l.Key()] {
				continue
			}
			seen[l.Key()] = true
			bySystem[l.System] = append(bySystem[l.System], l)
		}
	}

	// Probe each system once. results maps a link key to its Result.
	results := map[string]Result{}
	for system, ls := range bySystem {
		// Cap the per-system fan-out. Anything beyond the cap renders unchecked
		// so the report still accounts for it, but it never reaches a prober.
		probeLinks := ls
		if len(ls) > maxLinksPerSystem {
			probeLinks = ls[:maxLinksPerSystem]
			for _, l := range ls[maxLinksPerSystem:] {
				results[l.Key()] = Result{Unchecked: true, Reason: ReasonTooMany}
			}
		}

		prober, ok := reg.For(system)
		if !ok {
			for _, l := range probeLinks {
				results[l.Key()] = Result{Unchecked: true, Reason: reasonForUnprobed(system)}
			}
			continue
		}
		out, err := prober.Probe(ctx, probeLinks, since)
		if err != nil {
			reason := ReasonError
			if ctx.Err() != nil {
				reason = ReasonTimeout
			}
			for _, l := range probeLinks {
				results[l.Key()] = Result{Unchecked: true, Reason: reason}
			}
			continue
		}
		// A key the prober was asked about but omitted is unchecked, never a
		// silent no-change.
		for _, l := range probeLinks {
			if r, ok := out[l.Key()]; ok {
				results[l.Key()] = r
			} else {
				results[l.Key()] = Result{Unchecked: true, Reason: ReasonError}
			}
		}
	}

	// Reconcile per task and build the next watermark from checked links only.
	next := sources.Watermark{}
	report := Report{Tasks: map[string]TaskReport{}}

	for _, task := range tasks {
		lastLogged, hasLog := newestComment(task)
		baseline := lastLogged
		if !hasLog {
			baseline = task.UpdatedAt
		}

		tr := TaskReport{Title: task.Title, HasWorkLog: hasLog}
		if hasLog {
			ll := lastLogged
			tr.LastLogged = &ll
		}

		for _, l := range taskLinks[task.ID] {
			res := results[l.Key()]
			lf := LinkFreshness{Key: l.Key(), System: string(l.System), Raw: l.Raw}
			if res.Unchecked {
				lf.Unchecked = true
				lf.Reason = res.Reason
			} else {
				lf.LastActivity = res.LastActivity
				if res.LastActivity != nil {
					// Strict After: activity exactly at the baseline (the newest
					// work-log comment, or UpdatedAt when there is none) counts
					// as already logged, so re-running does not re-flag it.
					lf.Changed = res.LastActivity.After(baseline)
					next[l.Key()] = *res.LastActivity
				}
			}
			tr.Links = append(tr.Links, lf)
		}

		report.Tasks[task.ID] = tr
	}

	// Fold in any prior watermarks the run did not touch, so a warm run keeps
	// history for links that were not probed this pass.
	keys := make([]string, 0, len(since))
	for k := range since {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := next[k]; !ok {
			next[k] = since[k]
		}
	}

	return report, next, nil
}

// newestComment returns the newest comment PostedAt and whether the task has
// any comments at all.
func newestComment(task sources.Task) (time.Time, bool) {
	if len(task.Comments) == 0 {
		return time.Time{}, false
	}
	newest := task.Comments[0].PostedAt
	for _, c := range task.Comments[1:] {
		if c.PostedAt.After(newest) {
			newest = c.PostedAt
		}
	}
	return newest, true
}
