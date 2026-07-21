# Ballpoint triage-walk TUI design

Issue #5. A Bubbletea TUI that walks scoped Todoist tasks, renders cards from the
local cache with no network during the walk, and applies the triage keyword
lexicon locally. Mutations shell out to the existing tested macro scripts. Outward
sends are only queued, never sent from the TUI.

## Decisions (locked with the issue owner)

1. **Input model: single key plus argument prompt.** Each keyword binds to one key.
   Zero-argument verbs (`next`, `back`, `skip`, `more`, `open`, `quit`, `?`) fire on
   the keypress. Verbs that need text (`log`, `defer`, `col`, `prio`, `link`,
   `fixref`, `escalate`, `draft`) open a one-line prompt seeded with the verb, so
   the keystroke is the approval for the internal tier. This matches the lexicon's
   "typing a keyword is the approval" and the "acts immediately on keypress"
   criterion.
2. **Corpus: strictly offline, populated by `probe`.** `probe` already fetches the
   full task corpus and computes the freshness report; it just does not persist
   them. Extend `probe` to write each task and the report to the cache. The TUI
   reads only the cache and never fetches during a walk. The issue #4 prewarm timer
   runs `probe` on a schedule, so the cache stays warm and every relaunch is
   instant. The TUI shows the cache age and offers a one-key refresh (shells out to
   `ballpoint probe`) when the cache is empty or stale, so a cold first run or a
   long-idle pivot is not a dead end.
3. **Scope entry: flags plus interactive picker.** Flags/args resolve a scope for
   scripting and an SSH-from-phone path. A bare `ballpoint` with no scope opens a
   `huh` picker. Scope filters the cached corpus locally; nothing is fetched for a
   scope during the walk.

## Architecture

New package `internal/tui`. `internal/cli` gains a `walk` wiring that replaces the
`ErrNotImplemented` on the bare verb. The store gains a corpus enumerator and a
report round-trip. `probe` gains task+report persistence. A new `internal/queue`
package defines the dispatch queue file that issue #6 will consume.

```
cmd/ballpoint (bare, no verb)
  -> cli.runWalk(scope, deps)
       -> store.Open, LoadAllTasks, LoadReport, LoadWatermark   (cache only)
       -> scope.Resolve(flags | huh picker) -> []taskID
       -> tui.Run(model{cards, cursor, queue, session})
              cards render from cache (lipgloss layout, glamour work-logs)
              keyword keypress -> action
                internal  -> shell out to scripts/*.sh (synchronous), then reload that task
                completion -> huh confirm -> shell out
                outward    -> queue.Append(entry)  (never sends)
              resume: session.json persists cursor + scope
```

### Data sources (all read from cache, `internal/store`)

- `store.LoadAllTasks() ([]sources.Task, error)` (new): enumerate `<root>/cache/*.json`.
- `store.LoadReport() (probe.Report, bool, error)` (new): the freshness overlay,
  persisted by the probe run at `<root>/report.json`.
- `store.LoadWatermark()`: existing.
- The probe run (`internal/cli/probe.go`) gains: `SaveTask` per fetched task, and
  `SaveReport(report)` after the pass. `SaveReport` writes `<root>/report.json`
  atomically, mirroring `SaveWatermark`.

### The card (from `sources.Task` + `probe.TaskReport`)

Header: title, project / section (column), priority, due, ball owner and days
silent (derived), the last logged next-step. Body: description and work-log
comments rendered as Markdown through `glamour` in a `viewport` (scrollable, links
preserved, not flattened). Footer: one line per link with its freshness delta
(`moved` when `LinkFreshness.Changed`, the reason string when `Unchecked`, else the
age since `LastActivity`). Cards whose report has any `Changed` link sort first;
nothing is hidden (a stable sort keeps the rest in scope order).

Untrusted content: task title, description, labels, and comment bodies are
collaborator-controllable. The TUI only renders them; it never sends them to a
model, so fencing is not required here, but rendering goes through glamour which
neutralises terminal escape sequences.

### Keyword handling and gating

The keymap mirrors `references/lexicon.md`. Internal verbs run immediately (open a
prompt for their argument, then shell out). Completion verbs (`done`, `drop`,
`merge`) go through a `huh` confirm. Outward verbs (`nudge`, `email`, `teams`) are
never sent; they append to the dispatch queue and log a "queued for dispatch"
breadcrumb. `dig` and the navigation verbs are read-only.

Mutations shell out to `~/.claude/skills/todoist-triage/scripts/*.sh` with the
task ref and the parsed arguments. The script path root is resolved once (a
`--scripts-dir` flag with a sensible default), so the binary does not hardcode a
home path. A failed script surfaces its stderr in a status line and does not
advance; the card reloads from cache after a success so the new work-log shows.

### The dispatch queue (`internal/queue`, consumed by issue #6)

Outward actions append a JSON line to `<root>/queue/pending.jsonl`. Each entry:

```go
type Entry struct {
	ID        string    `json:"id"`         // ULID-free: taskID + "-" + monotonic counter
	TaskID    string    `json:"task_id"`
	TaskRef   string    `json:"task_ref"`   // the human ref the macros take
	Channel   string    `json:"channel"`    // slack | email | teams
	To        string    `json:"to"`
	Body      string    `json:"body"`
	QueuedAt  time.Time `json:"queued_at"`
}
```

Append-only, one JSON object per line, so issue #6's dispatcher can stream it and
the TUI never has to rewrite the file. `queue.Append(root, Entry)` and
`queue.Load(root)` are the surface; #6 owns draining.

### Resume

`session.json` at the state root holds the active scope descriptor and the cursor
(the current task id). On launch with the same scope, the cursor is restored to
that task if it is still present, else the nearest following task, else the start.
Writing is debounced to card transitions, not every keystroke.

### Resize

The Bubbletea `WindowSizeMsg` re-lays the three regions (header fixed, body
viewport flexes, footer fixed) via lipgloss. glamour is re-rendered at the new
width. No manual cursor math; lipgloss owns wrapping.

### SSH-from-phone (design-for, not build)

Nothing in the model reads the controlling TTY directly or assumes a local
display. The program takes an `io` in/out pair, so a later `ballpoint walk --serve`
over `wish` (Charm's SSH server) can host the same model. Not built now, just not
ruled out.

## Out of scope (issue #6 and the skill)

- Draining the dispatch queue and sending. The TUI only appends.
- The judgement/gating skill layer. The TUI is the fast path for the mechanical
  keyword tier, not a replacement for the model-driven walk.
