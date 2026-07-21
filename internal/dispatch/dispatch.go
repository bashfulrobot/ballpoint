package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// Config is everything Run needs. The two shell-outs (Assess, RunScript) and
// the clock (Now) are injected so the orchestrator is tested with fakes; the
// store, report, and queue root are real.
type Config struct {
	Store       *store.Store
	Root        string
	Report      probe.Report
	Entries     []queue.Entry
	ScriptsDir  string
	Concurrency int
	DryRun      bool
	Now         func() time.Time
	Assess      func(ctx context.Context, prompt string) (Assessment, float64, error)
	RunScript   func(ctx context.Context, argv []string) error
	Stdout      io.Writer
}

// Summary tallies the run for the CLI report.
type Summary struct {
	Succeeded int
	Failed    int
	Requeued  int
	Skipped   int
}

// taskGroup groups a task id with the queue entries that named it.
type taskGroup struct {
	id      string
	ref     string
	entries []queue.Entry
}

// outcome is one job's result, collected by index.
type outcome int

const (
	outSucceeded outcome = iota
	outFailed
	outRequeued
	outSkipped
)

// Run groups the queue by task, runs one bounded-concurrency job per task, and
// returns a tally. On a usage limit it cancels the remaining jobs, leaves every
// unfinished task's entries queued, and returns without error.
func Run(ctx context.Context, cfg Config) (Summary, error) {
	groups := groupByTask(cfg.Entries)
	if len(groups) == 0 {
		_, _ = fmt.Fprintln(cfg.Stdout, "nothing queued")
		return Summary{}, nil
	}

	if cfg.DryRun {
		return runDry(cfg, groups)
	}

	limit := cfg.Concurrency
	if limit < 1 {
		limit = 1
	}
	outcomes := make([]outcome, len(groups))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(limit)
	for i := range groups {
		i := i
		group.Go(func() error {
			out, usage := runJob(gctx, cfg, groups[i])
			outcomes[i] = out
			if usage {
				// Cancel the rest; they will see gctx done and requeue.
				return ErrUsageLimit
			}
			return nil
		})
	}
	// Wait never returns anything but ErrUsageLimit or nil; jobs handle their
	// own errors and never propagate them.
	_ = group.Wait()

	var sum Summary
	for _, o := range outcomes {
		switch o {
		case outSucceeded:
			sum.Succeeded++
		case outFailed:
			sum.Failed++
		case outRequeued:
			sum.Requeued++
		case outSkipped:
			sum.Skipped++
		}
	}
	return sum, nil
}

// groupByTask collapses entries to one group per task, preserving first-seen
// order so the run is deterministic.
func groupByTask(entries []queue.Entry) []taskGroup {
	index := map[string]int{}
	var groups []taskGroup
	for _, e := range entries {
		if i, ok := index[e.TaskID]; ok {
			groups[i].entries = append(groups[i].entries, e)
			continue
		}
		index[e.TaskID] = len(groups)
		groups = append(groups, taskGroup{id: e.TaskID, ref: "id:" + e.TaskID, entries: []queue.Entry{e}})
	}
	return groups
}

// runJob assesses one task and writes back. The bool return is true only when
// the job hit the usage limit, which tells Run to cancel the rest.
func runJob(ctx context.Context, cfg Config, g taskGroup) (outcome, bool) {
	now := cfg.Now()
	// A job that never starts because the pool was cancelled requeues.
	if ctx.Err() != nil {
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateRequeued, StartedAt: now, EndedAt: now})
		return outRequeued, false
	}

	task, ok, err := cfg.Store.LoadTask(g.id)
	if err != nil || !ok {
		detail := "task not in cache"
		if err != nil {
			detail = err.Error()
		}
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: detail, StartedAt: now, EndedAt: now})
		return outFailed, false
	}

	writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateRunning, StartedAt: now})

	nonce, err := newNonce()
	if err != nil {
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: err.Error(), StartedAt: now, EndedAt: cfg.Now()})
		return outFailed, false
	}
	prompt := BuildPrompt(task, cfg.Report.Tasks[g.id], nonce)

	assessment, cost, err := cfg.Assess(ctx, prompt)
	if err != nil {
		// An explicit usage limit, or a cancelled context because another job
		// hit the limit, means requeue; anything else is a real failure.
		if errors.Is(err, ErrUsageLimit) {
			writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateRequeued, StartedAt: now, EndedAt: cfg.Now()})
			return outRequeued, true
		}
		if ctx.Err() != nil {
			writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateRequeued, StartedAt: now, EndedAt: cfg.Now()})
			return outRequeued, false
		}
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: err.Error(), CostUSD: cost, StartedAt: now, EndedAt: cfg.Now()})
		return outFailed, false
	}

	// Writeback (network) before drain (local). A failure here leaves the queue
	// untouched, so the task retries on the next run.
	if err := cfg.RunScript(ctx, WorklogArgv(cfg.ScriptsDir, g.ref, assessment)); err != nil {
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: err.Error(), CostUSD: cost, StartedAt: now, EndedAt: cfg.Now()})
		return outFailed, false
	}
	for _, e := range g.entries {
		if e.Channel == "" || e.To == "" || e.Body == "" {
			continue // malformed draft, nothing to log
		}
		if err := cfg.RunScript(ctx, DraftArgv(cfg.ScriptsDir, g.ref, e)); err != nil {
			writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: err.Error(), CostUSD: cost, StartedAt: now, EndedAt: cfg.Now()})
			return outFailed, false
		}
	}

	ids := map[string]bool{}
	for _, e := range g.entries {
		ids[e.ID] = true
	}
	if _, err := queue.Remove(cfg.Root, ids); err != nil {
		writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateFailed, Detail: "drain: " + err.Error(), CostUSD: cost, StartedAt: now, EndedAt: cfg.Now()})
		return outFailed, false
	}

	writeStatus(cfg, Status{TaskID: g.id, TaskRef: g.ref, State: StateSucceeded, CostUSD: cost, StartedAt: now, EndedAt: cfg.Now()})
	return outSucceeded, false
}

// runDry prints each task's prompt and planned writes without invoking or
// draining anything.
func runDry(cfg Config, groups []taskGroup) (Summary, error) {
	for _, g := range groups {
		task, ok, err := cfg.Store.LoadTask(g.id)
		if err != nil || !ok {
			_, _ = fmt.Fprintf(cfg.Stdout, "task %s: not in cache, would fail\n\n", g.id)
			continue
		}
		prompt := BuildPrompt(task, cfg.Report.Tasks[g.id], "DRYRUN")
		_, _ = fmt.Fprintf(cfg.Stdout, "=== task %s prompt ===\n%s\n", g.id, prompt)
		_, _ = fmt.Fprintf(cfg.Stdout, "=== task %s planned writes ===\n", g.id)
		_, _ = fmt.Fprintf(cfg.Stdout, "worklog: %v\n", WorklogArgv(cfg.ScriptsDir, g.ref, Assessment{Summary: "<assessment>"}))
		for _, e := range g.entries {
			if e.Channel == "" || e.To == "" || e.Body == "" {
				continue
			}
			_, _ = fmt.Fprintf(cfg.Stdout, "draft: %v\n", DraftArgv(cfg.ScriptsDir, g.ref, e))
		}
		_, _ = fmt.Fprintln(cfg.Stdout)
	}
	return Summary{Skipped: len(groups)}, nil
}

// writeStatus persists a status, swallowing the error: a status write failure
// must not change a job's outcome, and the run summary is the source of truth.
func writeStatus(cfg Config, s Status) {
	_ = WriteStatus(cfg.Root, s)
}
