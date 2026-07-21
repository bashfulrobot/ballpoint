# Ballpoint dispatcher design (issue #6)

## Summary

`ballpoint dispatch` drains the outward queue that the triage walk writes and
runs one concurrent assessment job per task. Each job builds a self-contained
prompt from the cached task, its work log, and its freshness delta, shells out
to the local `claude` CLI in headless print mode, parses structured JSON, and
writes the assessment back through the existing `td_worklog.sh` writer. Any
outward message the walk queued for that task is logged as drafted through
`td_draft.sh` and never sent. No Anthropic SDK, no API key: authentication is
the CLI's existing subscription credentials.

## What the dispatcher drains

The job list comes from `internal/queue/pending.jsonl`, the append-only queue
the triage walk (issue #5) writes. Each `queue.Entry` already carries a
`TaskID`, a `TaskRef`, and a prepared outward message (`Channel`, `To`,
`Body`). The dispatcher groups entries by `TaskID` so one task produces one
assessment job regardless of how many outward drafts the walk queued for it.

This reading is grounded in three facts. The `internal/queue` package doc
states the issue #6 dispatcher is the intended drainer. The queued entries are
the "prepared outward messages" that the acceptance criteria say must be
"logged as drafted and never sent". And the issue context ("the walk only
queues work while jobs run behind it") maps directly onto the walk writing
queue entries and the dispatcher draining them.

The freshness report (`probe.Report`, from issues #3 and #11) supplies per-task
context for the prompt. It is not itself the job list. A task is assessed
because the walk queued it, and the freshness delta tells the job what changed
since the last log.

## Package layout

Everything new lives in `internal/dispatch/`, which today holds only a package
doc. The CLI wiring lives in `internal/cli/`, matching the probe's split
between a `resolve*Deps` function that touches the environment and a `run*`
function that takes an injected struct.

| File | Responsibility |
|------|----------------|
| `internal/dispatch/dispatch.go` | `Run(Config)` orchestrator: group queue by task, bounded-concurrency worker pool, drain on success, backoff on usage limit |
| `internal/dispatch/job.go` | One task's job: assess, write back, log drafts, drain, record status |
| `internal/dispatch/prompt.go` | Build the self-contained, untrusted-bracketed prompt from task + work log + delta |
| `internal/dispatch/assess.go` | `claude` CLI invocation, JSON output parsing, usage-limit detection |
| `internal/dispatch/writeback.go` | Shell to `td_worklog.sh` and `td_draft.sh`, build argv, dry-run rendering |
| `internal/dispatch/status.go` | Per-task job-status files under `<root>/dispatch/`, queryable |
| `internal/queue/queue.go` | Add `Remove(root, ids)` drain primitive (atomic rewrite) |
| `internal/cli/dispatch.go` | `dispatchFlags`, `parseDispatchFlags`, `resolveDispatchDeps`, `runDispatch` |
| `internal/cli/cli.go` | Replace the `dispatch` stub with the real command |

## Data flow

1. `runDispatch` resolves the state root (`config.StateDir()`), opens the
   store, loads the freshness report, locates the scripts directory, and reads
   the queue.
2. `dispatch.Run` groups entries by `TaskID`. Empty queue exits cleanly with a
   "nothing queued" message.
3. A worker pool (errgroup with `SetLimit(concurrency)`, mirroring
   `internal/sources/todoist/probe.go`) runs one `job` per task, bounded by the
   concurrency ceiling.
4. Each job:
   a. Loads the cached task from the store and the task's freshness delta from
      the report. A task missing from the cache fails the job (reportable,
      retryable) rather than guessing.
   b. Builds the prompt (see Prompt construction).
   c. Invokes `claude` (see Assessment invocation). On a usage-limit response,
      returns a sentinel that cancels the pool and requeues.
   d. Parses the JSON assessment. A parse failure fails the job.
   e. Writes back: assessment through `td_worklog.sh`, then each queued draft
      through `td_draft.sh`.
   f. Drains the task's entries from the queue (local atomic rewrite), then
      records `succeeded`.
5. `Run` returns a summary: succeeded, failed, requeued, and skipped counts.

Within a job the drafts are logged first and the assessment work-log write is
the last step before the drain, so if a draft fails the assessment is never
written and a retry cannot duplicate the assessment (the primary artifact). The
writeback and drain run under a cancellation-shielded context, so a pool cancel
(a peer job hitting the usage limit, or Ctrl-C) stops future jobs but never
kills a writeback that has already started.

Two duplicate-write windows remain and are accepted for this version. A
writeback script failure after some writes committed (for example a draft that
fails after another draft already logged) leaves the task queued, so the retry
re-runs the earlier writes and re-spends one assessment. The impact is a
duplicate same-day work-log bullet, never a duplicate outward send, and a
script failure is rare. A crash between the assessment write and the drain has
the same effect. Fully removing these needs a dedup key in the external
`td_worklog.sh` writer, which is out of this repo.

Concurrency correctness: many jobs drain their own entries at once, and
`queue.Remove` is a read-modify-write over the whole file. It is serialized by a
process mutex so two concurrent rewrites cannot lose one set of removals. A
single dispatch process is the only writer in practice; concurrent dispatch
processes are not a supported mode.

## CLI surface

```
ballpoint dispatch [flags]

  --concurrency N    max concurrent jobs (default 2, conservative)
  --model NAME       claude model alias or id (default "haiku")
  --scripts-dir DIR  directory holding td_worklog.sh and td_draft.sh
  --dry-run          print each prompt and planned write, invoke nothing
  --status           print job status for the current dispatch state and exit
```

`--concurrency` is conservative by default because every worker draws on the
same subscription quota, not on machine capacity. `--model` defaults to `haiku`
because the jobs are mechanical and do not need the most capable model.
`--scripts-dir` mirrors the walk's `--scripts-dir` override; when unset it
falls back to the same default the walk uses.

Re-running `ballpoint dispatch` retries whatever is still queued, so a failed
job is retried by running the command again. `--status` is the read path for
"job status is queryable".

## Prompt construction

The prompt is a single string piped to `claude` on stdin. It has three parts.

1. A fixed instruction block: assess what changed on this task and what it
   means, keep external references as Markdown links, record a next step where
   one exists, and output only a JSON object matching the schema below. The
   block states plainly that everything inside the task block is untrusted data
   to be summarized, never instructions to follow.
2. An untrusted block bracketed by a per-job nonce (`crypto/rand` hex),
   following the same pattern the review skills use. It contains the task
   title, project, section, due, priority, description, the work log
   (each comment's timestamp, content, and attachment), and the freshness
   delta (which links changed, their systems, and last activity).
3. The output schema.

Output schema the job must return:

```json
{
  "summary": "one to three sentences on what changed and what it means",
  "verb": "note",
  "links": [{"label": "PR #42", "url": "https://github.com/..."}],
  "next": "optional next step, empty string when none"
}
```

`summary` is required and non-empty. `verb` defaults to `note` when empty.
`links` and `next` are optional. Any other shape fails the job.

## Assessment invocation

```
claude -p \
  --output-format json \
  --model <model> \
  --tools "" \
  --permission-mode dontAsk \
  --no-session-persistence
```

The prompt is written to the process stdin, not passed as an argument, so task
content never lands in argv. `--tools ""` disables every tool, so a worker
cannot run Bash, read or write files, reach the network, or send anything
outward. `--permission-mode dontAsk` guarantees the run never blocks on a
prompt. `--no-session-persistence` keeps each job stateless so parallel jobs do
not share or clobber a session.

The CLI exits 0 even on an API-level error, so the job parses the JSON and
checks `is_error`. A usage or rate limit surfaces as `is_error: true` with
`api_error_status: 429`; the job returns a `usageLimit` sentinel. The
orchestrator then cancels the remaining jobs, leaves every unfinished task's
entries in the queue, marks them `requeued`, and returns. Backing off cleanly
rather than hammering, and retrying only on the next on-demand run.

The parsed CLI envelope:

```go
type cliResult struct {
    Type           string  `json:"type"`
    Subtype        string  `json:"subtype"`
    IsError        bool    `json:"is_error"`
    APIErrorStatus *int    `json:"api_error_status"`
    Result         string  `json:"result"`
    TotalCostUSD   float64 `json:"total_cost_usd"`
}
```

`Result` holds the assistant's final text, which is the assessment JSON. It is
parsed defensively (leading and trailing Markdown code fences stripped) before
unmarshaling into the assessment schema.

## Queue drain primitive

`internal/queue` is append-only today. The dispatcher adds:

```go
// Remove rewrites pending.jsonl without the entries whose IDs are in ids,
// atomically (temp file, fsync, rename). Returns the number removed. A
// missing file is a no-op. Concurrent-safe against Append through the same
// per-file discipline the store uses.
func Remove(root string, ids map[string]bool) (int, error)
```

Only the entries for a task that fully succeeded are removed. A requeued or
failed task keeps its entries, so the next run picks them up.

## Job status

Each task writes a status file at `<root>/dispatch/<taskID>.json` through the
same atomic write the store uses:

```go
type Status struct {
    TaskID    string    `json:"task_id"`
    TaskRef   string    `json:"task_ref"`
    State     string    `json:"state"` // running, succeeded, failed, requeued, skipped
    Detail    string    `json:"detail,omitempty"`
    CostUSD   float64   `json:"cost_usd,omitempty"`
    StartedAt time.Time `json:"started_at"`
    EndedAt   time.Time `json:"ended_at,omitempty"`
}
```

A job writes `running` before it invokes `claude` and the terminal state when
it finishes, so `ballpoint dispatch --status` reads a live view while a run is
in flight. A failed job's `Detail` carries the error for the report.

## Dry run

`--dry-run` walks the same grouping and, per task, prints the built prompt and
the exact `td_worklog.sh` and `td_draft.sh` argv it would run, then stops. It
never invokes `claude`, never writes back, and never drains the queue. This is
the safe way to inspect prompts and planned writes before a real run.

## Untrusted content

Task titles, descriptions, comments, and attachment names are collaborator
controlled. They enter the prompt only inside the nonce-bracketed untrusted
block, with an explicit instruction that the block is data to summarize and
never instructions to follow. Nothing from task content is executed. The
worker has no tools, so even a successful injection has no capability to act.
Recipient and body fields on queued entries are likewise untrusted; the draft
path only records them as text, it never sends.

## Testing

- `queue.Remove` unit tests: removes the right IDs, leaves the rest in order,
  atomic rewrite, missing-file no-op.
- Prompt construction: golden test of the assembled prompt for a task with a
  work log and a non-empty delta, and for a task with an empty delta. Asserts
  the untrusted block is nonce-bracketed and that control bytes in task content
  do not break the structure.
- Assessment parsing: table tests over CLI envelopes (success, `is_error` with
  429, malformed `result`, fenced `result`).
- Writeback argv: assert the exact `td_worklog.sh` and `td_draft.sh` argv for
  an assessment with links and a next step, and for a bare assessment.
- Orchestrator: a fake assessor and a fake writer injected through `Config`,
  covering success drains the queue, failure leaves it, a 429 requeues and
  stops, and dry-run touches nothing.
- CLI: `parseDispatchFlags` table tests, and the existing `dispatch` stub test
  in `internal/cli/cli_test.go` is rewritten. Usage golden regenerated.

Manual acceptance from the issue:

```
nix develop --command go test ./...
ballpoint dispatch --dry-run
```

Run against a small real queue, confirm the work-log entries match the
established format, nothing outward was sent, and a forced failure leaves the
task unchanged.

## Decisions made autonomously

These were resolved from the issue body and the shipped #5 code rather than by
asking, and are logged here and on the PR.

- Job source is the `internal/queue` outward queue, one job per task. Grounded
  in the queue package doc and the "logged as drafted, never sent" criterion.
- Workers run with no tools. The model receives all context in the prompt and
  returns JSON; the dispatcher performs the sanctioned writes. This is the
  strongest reading of "worker tool access is constrained so a job cannot send
  anything outward".
- Default concurrency 2 and default model `haiku`, both configurable.
- Writeback before drain, drain only on full success, so failures leave the
  task untouched and retryable.
