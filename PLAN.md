# Surface dispatcher assessments on the walk + prewarm implementation plan

**Goal:** Make the dispatcher's AI assessment present on the walk ahead of time, without ever putting AI on the interactive path, and schedule the dispatcher so assessments exist before a walk.

**Architecture:** Persist the assessment summary into the per-task dispatch status file the dispatcher already writes. The walk loads those statuses at resolve time (the same place it loads tasks and the report) and carries each summary on its Card, rendered as a distinct section above the work log. A new opt-in systemd timer runs `ballpoint dispatch` after the probe timer.

**Tech stack:** Go, Home Manager / systemd user units, Nix.

## Task 1: persist the assessment (`internal/dispatch/status.go`, `dispatch.go`, tests)

- Add `Assessment string json:"assessment,omitempty"` to `dispatch.Status`.
- In `runJob`'s success path (`dispatch.go:222`), set `Assessment: assessment.Summary` on the `StateSucceeded` status. No other status carries it.
- TDD: a status round-trips the assessment through `WriteStatus`/`LoadStatuses`; a run that succeeds writes the summary (drive `dispatch.Run` with a fake Assess in the existing dispatch_test, assert the status file carries the summary).

## Task 2: carry it on the Card (`internal/tui/card.go`, `run.go`, tests)

- Add `Assessment string` to `Card`.
- In `ResolveWalk` (`run.go`), call `dispatch.LoadStatuses(cfg.StateDir)`, index the latest summary by task id (statuses are already sorted; a task has one file), and set `card.Assessment` when a non-empty summary exists. A missing dispatch dir is empty, not an error (LoadStatuses already returns nil for that).
- Keep `BuildCard` signature unchanged; set `Assessment` on the returned card in `ResolveWalk` so the pure card builder stays report-only.
- TDD: `ResolveWalk` with a written dispatch status surfaces the summary on the matching card; a card with no status has an empty `Assessment`.

## Task 3: render it (`internal/tui/view.go`, tests)

- In `workLogMarkdown`, when `c.Assessment != ""`, prepend an `## Assessment` section with the sanitized summary, above the description and work log. Absent assessment renders nothing.
- The summary is model-produced text, so run it through `sanitizeTerminal` like every other body string before it reaches glamour.
- TDD: `workLogMarkdown` includes the assessment heading and text when present, and omits the heading entirely when absent.

## Task 4: prewarm the dispatcher (`nix/hm-module.nix`)

- Add `programs.ballpoint.dispatch` options (opt-in `enable`, `onCalendar`, `onStartupSec`, `randomizedDelaySec`, `restartSec`, `startLimit*`, `concurrency`, `model`). Dispatch reads the cached report and queue, so it needs no secrets path.
- Add `systemd.user.services.ballpoint-dispatch` (oneshot, `ExecStart` = `ballpoint dispatch` with optional `--concurrency`/`--model`) ordered `After = [ "ballpoint-probe.service" ]`, and `systemd.user.timers.ballpoint-dispatch`.
- Reuse `escapeSystemdExecArgs` and `assertNoNewline`. Default cadence a bit after the probe schedule.

## Task 5: verify

- `git add`, then `nix develop --command go test ./...`, `golangci-lint run ./...`, `nix build`, `nix flake check`.
