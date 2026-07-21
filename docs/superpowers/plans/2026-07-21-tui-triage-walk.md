# Triage-Walk TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Bubbletea TUI that walks scoped Todoist tasks offline from the cache, renders cards (with Markdown work-logs), applies the triage keyword lexicon (single key plus an argument prompt), shells out to the existing macro scripts for mutations, queues outward sends for issue #6, and resumes where it left off.

**Architecture:** Build bottom-up. The pure core (store enumerator, probe persistence, queue, card derivation, scope resolution, keymap/parser, macro runner) is unit-tested without a terminal. The Bubbletea `Model` sits on top and is tested by driving `Update` with messages. See `docs/superpowers/specs/2026-07-21-tui-triage-walk.md`.

**Tech Stack:** Go 1.26, charmbracelet bubbletea/bubbles/lipgloss/glamour/huh (already in go.mod).

---

## File Structure

- `internal/store/store.go` — add `LoadAllTasks`, `SaveReport`, `LoadReport`. Modify.
- `internal/cli/probe.go` — persist tasks and report in the probe run. Modify.
- `internal/queue/queue.go` — dispatch queue (Entry, Append, Load). Create.
- `internal/tui/card.go` — derive card view-model from Task + TaskReport. Create.
- `internal/tui/scope.go` — scope descriptor, resolve to task ids from cache. Create.
- `internal/tui/keymap.go` — lexicon keymap, argument parsing, gating tier. Create.
- `internal/tui/macro.go` — resolve scripts dir, run a macro script, capture result. Create.
- `internal/tui/session.go` — resume state (session.json). Create.
- `internal/tui/model.go` — the Bubbletea model, layout, update loop. Create.
- `internal/tui/run.go` — `Run(cfg)` entrypoint that builds deps and starts the program. Create.
- `internal/cli/cli.go` + `internal/cli/walk.go` — wire the bare verb to the walk. Modify/Create.

---

## Task 1: Store enumerator and report round-trip

**Files:** Modify `internal/store/store.go`, `internal/store/store_test.go`.

- [ ] **Step 1: Failing tests**

Add to `internal/store/store_test.go`:

```go
func TestLoadAllTasks(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2", "3"} {
		if err := s.SaveTask(sources.Task{ID: id, Title: "t" + id}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoadAllTasks()
	if err != nil {
		t.Fatalf("LoadAllTasks() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("LoadAllTasks() returned %d tasks, want 3", len(got))
	}
}

func TestLoadAllTasksEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadAllTasks()
	if err != nil {
		t.Fatalf("LoadAllTasks() on empty cache error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadAllTasks() = %d, want 0 on empty cache", len(got))
	}
}

func TestReportRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LoadReport(); ok {
		t.Fatal("LoadReport() reported present on empty cache")
	}
	want := probe.Report{Tasks: map[string]probe.TaskReport{
		"1": {Title: "t1", Links: []probe.LinkFreshness{{Key: "slack:c:1", System: "slack", Changed: true}}},
	}}
	if err := s.SaveReport(want); err != nil {
		t.Fatalf("SaveReport() error = %v", err)
	}
	got, ok, err := s.LoadReport()
	if err != nil || !ok {
		t.Fatalf("LoadReport() ok=%v err=%v", ok, err)
	}
	if !got.Tasks["1"].Links[0].Changed {
		t.Errorf("LoadReport() lost the Changed flag: %+v", got)
	}
}
```

Add the `probe` import to the test file.

- [ ] **Step 2: Verify fail** — `nix develop --command go test ./internal/store/` fails (undefined methods).

- [ ] **Step 3: Implement**

In `internal/store/store.go` add (the cache dir is `s.cacheDir`, matching the existing `LoadTask`/`SaveTask`; confirm the field name when implementing and reuse it):

```go
// LoadAllTasks reads every cached task. A malformed or unreadable file is
// skipped rather than failing the whole walk, so one bad cache entry does not
// blank the queue. Order is not guaranteed; the caller sorts.
func (s *Store) LoadAllTasks() ([]sources.Task, error) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cache dir: %w", err)
	}
	tasks := make([]sources.Task, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, ok, err := s.LoadTask(id)
		if err != nil || !ok {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// reportPath is the persisted freshness overlay.
func (s *Store) reportPath() string { return filepath.Join(s.root, "report.json") }

// SaveReport writes the freshness report atomically, mirroring SaveWatermark.
func (s *Store) SaveReport(r probe.Report) error {
	return writeAtomic(s.reportPath(), r) // reuse the existing atomic writer; see SaveWatermark
}

// LoadReport reads the freshness report. ok is false when none has been written.
func (s *Store) LoadReport() (probe.Report, bool, error) {
	data, err := os.ReadFile(s.reportPath())
	if err != nil {
		if os.IsNotExist(err) {
			return probe.Report{}, false, nil
		}
		return probe.Report{}, false, fmt.Errorf("reading report: %w", err)
	}
	var r probe.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return probe.Report{}, false, fmt.Errorf("decoding report: %w", err)
	}
	return r, true, nil
}
```

Match the existing atomic-write helper's real name and the `s.root`/`s.cacheDir` field names (read `store.go` first). Add imports as needed (`strings`). Importing `internal/probe` from `internal/store`: confirm no import cycle (probe imports sources and links, not store; store importing probe is fine).

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/store/`.

- [ ] **Step 5: Commit** — `git commit -m "feat(store): enumerate cached tasks and persist the freshness report"`.

---

## Task 2: Persist tasks and report during the probe run

**Files:** Modify `internal/cli/probe.go`, `internal/cli/cli_test.go`.

- [ ] **Step 1: Failing test**

`runProbe` currently saves only the watermark. Add a test that after a real (non-dry-run) probe the cache holds the tasks and the report. `runProbe` takes injected `probeDeps` (tasks preloaded), so this needs no network:

```go
func TestRunProbePersistsCorpus(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	tasks := []sources.Task{{ID: "1", Title: "one"}, {ID: "2", Title: "two"}}
	if err := runProbe(probeDeps{tasks: tasks, stateDir: dir}, &stdout, &stderr); err != nil {
		t.Fatalf("runProbe() error = %v", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadAllTasks()
	if err != nil || len(got) != 2 {
		t.Fatalf("LoadAllTasks() = %d tasks, err=%v, want 2", len(got), err)
	}
	if _, ok, _ := st.LoadReport(); !ok {
		t.Error("probe did not persist a report")
	}
}
```

Add `store` import to the test.

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement.** In `runProbe` (`internal/cli/probe.go`), after opening the store and before/after the `probe.Run` call, persist. The store is already opened as `st` in the non-dry-run path:

```go
	// Persist the corpus so the TUI (issue #5) walks it offline. A single task
	// that fails to save is not fatal; the walk skips a missing card.
	for _, task := range deps.tasks {
		if err := st.SaveTask(task); err != nil {
			_, _ = fmt.Fprintf(stderr, "warning: caching task %s: %v\n", task.ID, err)
		}
	}
	// report is the *probe.Report returned by probe.Run.
	if err := st.SaveReport(report); err != nil {
		return err
	}
```

Place the task loop right after `store.Open` succeeds; place `SaveReport` right after `probe.Run` returns `report`. Read the current function to slot these in without disturbing the dry-run early return.

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/cli/`.

- [ ] **Step 5: Commit** — `git commit -m "feat(probe): persist the task corpus and report to the cache"`.

---

## Task 3: The dispatch queue package

**Files:** Create `internal/queue/queue.go`, `internal/queue/queue_test.go`.

- [ ] **Step 1: Failing test**

```go
package queue

import (
	"testing"
	"time"
)

func TestAppendAndLoad(t *testing.T) {
	root := t.TempDir()
	e1 := Entry{ID: "1-1", TaskID: "1", TaskRef: "DEVP-I-42", Channel: "slack", To: "#team", Body: "ping", QueuedAt: time.Unix(1, 0).UTC()}
	e2 := Entry{ID: "2-1", TaskID: "2", TaskRef: "CASE 5", Channel: "email", To: "x@y.z", Body: "hi", QueuedAt: time.Unix(2, 0).UTC()}
	if err := Append(root, e1); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, e2); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "1-1" || got[1].Channel != "email" {
		t.Fatalf("Load() = %+v, want the two appended entries in order", got)
	}
}

func TestLoadEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() on empty error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load() = %d, want 0", len(got))
	}
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement `internal/queue/queue.go`**

```go
// Package queue is the append-only dispatch queue the TUI writes and the issue
// #6 dispatcher drains. Outward actions (a Slack nudge, an email) are queued
// here and never sent from the TUI.
package queue

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one queued outward action, one JSON object per line.
type Entry struct {
	ID       string    `json:"id"`
	TaskID   string    `json:"task_id"`
	TaskRef  string    `json:"task_ref"`
	Channel  string    `json:"channel"`
	To       string    `json:"to"`
	Body     string    `json:"body"`
	QueuedAt time.Time `json:"queued_at"`
}

func dir(root string) string  { return filepath.Join(root, "queue") }
func file(root string) string { return filepath.Join(dir(root), "pending.jsonl") }

// Append adds one entry. The file is append-only so the dispatcher can stream it
// and the TUI never rewrites it.
func Append(root string, e Entry) error {
	if err := os.MkdirAll(dir(root), 0o700); err != nil {
		return fmt.Errorf("creating queue dir: %w", err)
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding queue entry: %w", err)
	}
	f, err := os.OpenFile(file(root), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening queue: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("appending to queue: %w", err)
	}
	return nil
}

// Load reads every queued entry in order. A missing file is an empty queue.
func Load(root string) ([]Entry, error) {
	f, err := os.Open(file(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening queue: %w", err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("decoding queue entry: %w", err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading queue: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/queue/`.

- [ ] **Step 5: Commit** — `git commit -m "feat(queue): append-only dispatch queue for outward actions"`.

---

## Task 4: Card derivation (pure view-model)

**Files:** Create `internal/tui/card.go`, `internal/tui/card_test.go`.

The card is a pure function of a `sources.Task` and its `probe.TaskReport`. This is the highest-value unit-tested logic: freshness lines, "moved" detection, days-silent, sort-moved-first.

- [ ] **Step 1: Failing tests**

```go
package tui

import (
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestBuildCardFreshnessLines(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	last := now.Add(-48 * time.Hour)
	task := sources.Task{ID: "1", Title: "fix repro", Project: "Kong", Section: "Engineering", Priority: "p2"}
	rep := probe.TaskReport{Links: []probe.LinkFreshness{
		{Key: "slack:c:1", System: "slack", Changed: true, LastActivity: &last},
		{Key: "aha:DEVP-I-42", System: "aha", Unchecked: true, Reason: probe.ReasonNoProbe},
	}}
	c := BuildCard(task, rep, now)
	if !c.Moved {
		t.Error("card with a Changed link should be Moved")
	}
	if len(c.Links) != 2 {
		t.Fatalf("want 2 link lines, got %d", len(c.Links))
	}
	if c.Links[0].State != LinkMoved {
		t.Errorf("slack link state = %v, want LinkMoved", c.Links[0].State)
	}
	if c.Links[1].State != LinkUnchecked || c.Links[1].Detail != string(probe.ReasonNoProbe) {
		t.Errorf("aha link = %+v, want unchecked with the reason", c.Links[1])
	}
}

func TestSortMovedFirstStable(t *testing.T) {
	now := time.Now()
	cards := []Card{
		{TaskID: "a", Moved: false},
		{TaskID: "b", Moved: true},
		{TaskID: "c", Moved: false},
		{TaskID: "d", Moved: true},
	}
	SortMovedFirst(cards)
	got := []string{cards[0].TaskID, cards[1].TaskID, cards[2].TaskID, cards[3].TaskID}
	want := []string{"b", "d", "a", "c"} // moved first, original order preserved within each group
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortMovedFirst order = %v, want %v", got, want)
		}
	}
	_ = now
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement `internal/tui/card.go`**

```go
package tui

import (
	"sort"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// LinkState is how a link's freshness renders in the footer.
type LinkState int

const (
	LinkFresh     LinkState = iota // checked, no new activity since the work-log
	LinkMoved                      // external activity newer than the work-log
	LinkUnchecked                  // could not be verified this run
)

// LinkLine is one rendered freshness row.
type LinkLine struct {
	System string
	State  LinkState
	Detail string // "moved", the age string, or the unchecked reason
}

// Card is the pure view-model for one task. Rendering (Task 8) turns it into
// styled text; this holds no styling.
type Card struct {
	TaskID string
	Task   sources.Task
	Report probe.TaskReport
	Moved  bool // any link moved; drives sort-first
	Links  []LinkLine
}

// BuildCard derives the view-model. now is injected so tests are deterministic.
func BuildCard(task sources.Task, rep probe.TaskReport, now time.Time) Card {
	c := Card{TaskID: task.ID, Task: task, Report: rep}
	for _, l := range rep.Links {
		line := LinkLine{System: l.System}
		switch {
		case l.Unchecked:
			line.State = LinkUnchecked
			line.Detail = string(l.Reason)
		case l.Changed:
			line.State = LinkMoved
			line.Detail = "moved"
			c.Moved = true
		default:
			line.State = LinkFresh
			if l.LastActivity != nil {
				line.Detail = humanizeAge(now.Sub(*l.LastActivity))
			}
		}
		c.Links = append(c.Links, line)
	}
	return c
}

// SortMovedFirst is a stable partition: moved cards first, original order kept
// within each group, so nothing is hidden and the rest stays in scope order.
func SortMovedFirst(cards []Card) {
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].Moved && !cards[j].Moved
	})
}

// humanizeAge renders a coarse age like "2d" or "3h" or "12m".
func humanizeAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return itoa(int(d/time.Hour)) + "h"
	default:
		return itoa(int(d/time.Minute)) + "m"
	}
}
```

Add a tiny `itoa` (use `strconv.Itoa`; import `strconv` and drop the helper) when implementing. Keep `humanizeAge` a plain function; do not over-abstract.

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/tui/`.

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): card view-model with freshness lines and moved-first sort"`.

---

## Task 5: Scope resolution

**Files:** Create `internal/tui/scope.go`, `internal/tui/scope_test.go`.

A `Scope` selects a task subset from the cached corpus with no network. Types: project, filter (saved or raw, treated the same at this layer, matched against a task field the cache carries), preset (a named set mapping to a filter), and single task by id. The raw-filter query language is Todoist's; at the cache layer, support the field-level predicates the cache can answer (project, priority, label, section) and pass anything else through as "match by substring in title" so it degrades rather than fetches. (A full Todoist filter parser is out of scope; note the limitation.)

- [ ] **Step 1: Failing tests**

```go
func TestScopeProject(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Project: "Kong"}, {ID: "2", Project: "Home"}, {ID: "3", Project: "Kong"},
	}
	ids := Scope{Kind: ScopeProject, Value: "Kong"}.Resolve(tasks)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "3" {
		t.Fatalf("project scope = %v, want [1 3]", ids)
	}
}

func TestScopeSingleTask(t *testing.T) {
	tasks := []sources.Task{{ID: "1"}, {ID: "2"}}
	ids := Scope{Kind: ScopeTask, Value: "2"}.Resolve(tasks)
	if len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("task scope = %v, want [2]", ids)
	}
}

func TestParseScopeFlags(t *testing.T) {
	s, ok := ParseScopeFlags(map[string]string{"project": "Kong"})
	if !ok || s.Kind != ScopeProject || s.Value != "Kong" {
		t.Fatalf("ParseScopeFlags = %+v ok=%v", s, ok)
	}
	if _, ok := ParseScopeFlags(map[string]string{}); ok {
		t.Error("ParseScopeFlags with no flags should report ok=false so the picker opens")
	}
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement `internal/tui/scope.go`**

```go
package tui

import (
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

type ScopeKind int

const (
	ScopeProject ScopeKind = iota
	ScopeFilter            // raw or saved filter query
	ScopePreset            // named preset -> a filter
	ScopeTask              // a single task id
	ScopeAll               // everything cached (fallback)
)

// Scope selects a task subset from the cached corpus.
type Scope struct {
	Kind  ScopeKind
	Value string
}

// Resolve filters the cached corpus. It never fetches; an unknown value simply
// yields an empty result, which the TUI reports rather than hiding.
func (s Scope) Resolve(tasks []sources.Task) []string {
	var ids []string
	for _, t := range tasks {
		if s.matches(t) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

func (s Scope) matches(t sources.Task) bool {
	switch s.Kind {
	case ScopeProject:
		return strings.EqualFold(t.Project, s.Value)
	case ScopeTask:
		return t.ID == s.Value
	case ScopeAll:
		return true
	case ScopeFilter, ScopePreset:
		// Degrade a raw filter to a title/label/section substring so the cache
		// can answer it without a Todoist filter parser. Documented limitation.
		q := strings.ToLower(s.Value)
		if strings.Contains(strings.ToLower(t.Title), q) {
			return true
		}
		for _, l := range t.Labels {
			if strings.Contains(strings.ToLower(l), q) {
				return true
			}
		}
		return strings.Contains(strings.ToLower(t.Section), q)
	}
	return false
}

// ParseScopeFlags maps CLI flags to a Scope. ok is false when no scope flag was
// given, so the caller opens the interactive picker.
func ParseScopeFlags(flags map[string]string) (Scope, bool) {
	for kind, key := range map[ScopeKind]string{
		ScopeProject: "project",
		ScopeFilter:  "filter",
		ScopePreset:  "preset",
		ScopeTask:    "task",
	} {
		if v, ok := flags[key]; ok && v != "" {
			return Scope{Kind: kind, Value: v}, true
		}
	}
	return Scope{}, false
}
```

Note in a comment that iterating a map is nondeterministic; since the flags are mutually exclusive in practice (the CLI only sets one), that is acceptable. If two are set, the CLI layer (Task 10) rejects it before calling this.

- [ ] **Step 4: Verify pass.**

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): scope resolution over the cached corpus"`.

---

## Task 6: Keyword keymap and argument parsing

**Files:** Create `internal/tui/keymap.go`, `internal/tui/keymap_test.go`.

Encodes the lexicon: each verb, its key, its tier, and whether it needs an argument. Pure, table-driven, fully testable.

- [ ] **Step 1: Failing tests**

```go
func TestVerbForKey(t *testing.T) {
	v, ok := VerbForKey('l')
	if !ok || v.Name != "log" || v.Tier != TierInternal || !v.NeedsArg {
		t.Fatalf("key l = %+v ok=%v, want log/internal/needs-arg", v, ok)
	}
	v, ok = VerbForKey('n')
	if !ok || v.Name != "next" || v.NeedsArg {
		t.Fatalf("key n = %+v, want next with no arg", v)
	}
	if _, ok := VerbForKey('Z'); ok {
		t.Error("unknown key should not resolve")
	}
}

func TestVerbTiers(t *testing.T) {
	done, _ := VerbForKey('D')
	if done.Name != "done" || done.Tier != TierCompletion {
		t.Errorf("D = %+v, want done/completion", done)
	}
	nudge, _ := VerbForName("nudge")
	if nudge.Tier != TierOutward {
		t.Errorf("nudge tier = %v, want outward", nudge.Tier)
	}
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement `internal/tui/keymap.go`**

Define `Tier` (TierInternal, TierCompletion, TierOutward, TierNav), `Verb{Name, Key rune, Tier, NeedsArg, Script string, Prompt string}`, a `verbs` table covering the full lexicon (log l, link L, defer d, col c, prio p, fixref f, escalate e, draft r; done D, drop X, merge M; nudge, email, teams as named outward verbs launched from `draft`-then-send flow or a menu; dig g; more m, open o, next n, skip s, back b, quit q, help ?). Provide `VerbForKey(r rune) (Verb, bool)` and `VerbForName(string) (Verb, bool)`. Map each internal/completion verb to its `scripts/*.sh` name from `references/lexicon.md` (log/link/fixref -> td_worklog.sh, defer -> td_defer.sh, col -> td_move.sh, prio -> td_reprioritize.sh, escalate -> td_escalate.sh, draft -> td_draft.sh, done -> td_complete.sh, drop -> td_drop.sh, merge -> td_merge.sh). Keys must be unique; the test above pins a few, add a `TestKeysUnique` that asserts no two verbs share a key.

- [ ] **Step 4: Verify pass** (including a `TestKeysUnique`).

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): lexicon keymap with gating tiers"`.

---

## Task 7: Macro runner

**Files:** Create `internal/tui/macro.go`, `internal/tui/macro_test.go`.

Resolves the scripts dir and runs a macro, capturing stdout/stderr/exit. Injectable runner so tests use a fake, mirroring how `internal/probe/salesforce` injects its `Runner`.

- [ ] **Step 1: Failing tests** — with an injected runner, assert the built argv for a `log` verb is `[<dir>/td_worklog.sh <ref> --entry <text>]`, and that a non-zero exit returns an error carrying stderr.

```go
func TestMacroArgv(t *testing.T) {
	var gotName string
	var gotArgs []string
	m := Macro{Dir: "/s", Run: func(name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return nil, nil
	}}
	err := m.Run2(Verb{Name: "log", Script: "td_worklog.sh"}, "DEVP-I-42", []string{"--entry", "chased eng"})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "/s/td_worklog.sh" {
		t.Errorf("script = %q, want /s/td_worklog.sh", gotName)
	}
	if gotArgs[0] != "DEVP-I-42" || gotArgs[1] != "--entry" {
		t.Errorf("args = %v", gotArgs)
	}
}
```

(Name the real method something clean like `Exec`, not `Run2`; the placeholder above is only to force the failing test. Pick the final name in Step 3 and use it in the test.)

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement** a `Macro{Dir string; Run func(name string, args ...string) ([]byte, error)}` with an `Exec(v Verb, ref string, args []string) error` that joins `Dir`+`v.Script`, prepends `ref`, calls `Run`, and wraps a non-zero exit with the captured output. Default `Run` uses `exec.Command(...).CombinedOutput()`. Add a `DefaultScriptsDir()` that returns `~/.claude/skills/todoist-triage/scripts` and a `--scripts-dir` override lands in Task 10.

- [ ] **Step 4: Verify pass.**

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): macro runner shelling out to the triage scripts"`.

---

## Task 8: Session resume

**Files:** Create `internal/tui/session.go`, `internal/tui/session_test.go`.

- [ ] **Step 1: Failing tests** — round-trip a `Session{Scope, Cursor}` to `session.json` under a temp root; a missing file yields a zero session with ok=false; restoring a cursor whose task is gone falls back to the nearest following id then the first.

```go
func TestSessionRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, ok, _ := LoadSession(root); ok {
		t.Fatal("empty root reported a session")
	}
	want := Session{Scope: Scope{Kind: ScopeProject, Value: "Kong"}, Cursor: "42"}
	if err := SaveSession(root, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadSession(root)
	if err != nil || !ok || got.Cursor != "42" || got.Scope.Value != "Kong" {
		t.Fatalf("LoadSession = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestResolveCursorFallback(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	if got := ResolveCursor(order, "c"); got != 2 {
		t.Errorf("present cursor -> %d, want 2", got)
	}
	if got := ResolveCursor(order, "zzz"); got != 0 {
		t.Errorf("absent cursor -> %d, want 0 (start)", got)
	}
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement** `Session`, `SaveSession` (atomic write of `session.json`), `LoadSession`, and `ResolveCursor(order []string, cursor string) int` returning the index of the cursor or 0. (Nearest-following is a refinement; start-at-0 is the honest fallback and satisfies the criterion "returns to the same position" for the common case where the task is still present.)

- [ ] **Step 4: Verify pass.**

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): resumable session state"`.

---

## Task 9: The Bubbletea model

**Files:** Create `internal/tui/model.go`, `internal/tui/model_test.go`.

The model composes the pieces. It is tested by driving `Update` with `tea.KeyMsg` and `tea.WindowSizeMsg` and asserting state transitions and the queued/exec side effects, without a real terminal.

- [ ] **Step 1: Failing tests** (behavioural, no TTY)

```go
func TestModelNextAdvances(t *testing.T) {
	m := newTestModel(t, 3) // 3 cards
	m2, _ := m.Update(key('n'))
	if m2.(Model).cursor != 1 {
		t.Errorf("cursor after next = %d, want 1", m2.(Model).cursor)
	}
}

func TestModelInternalVerbOpensPrompt(t *testing.T) {
	m := newTestModel(t, 1)
	m2, _ := m.Update(key('l')) // log needs an arg
	if !m2.(Model).prompting {
		t.Error("log should open the argument prompt")
	}
}

func TestModelOutwardQueuesNeverSends(t *testing.T) {
	root := t.TempDir()
	m := newTestModelAt(t, root, 1)
	// drive a draft->queue path; assert queue.Load(root) grows and no macro ran.
	// (exact keys per the final keymap)
	...
}

func TestModelResizeReflows(t *testing.T) {
	m := newTestModel(t, 1)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if m2.(Model).width != 40 {
		t.Error("resize did not update width")
	}
}
```

Provide `newTestModel`/`newTestModelAt` helpers building a Model with in-memory cards, an injected macro runner (records calls, never shells out), and a temp state root. `key(r rune) tea.KeyMsg` builds a key message.

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement `model.go`**

Model fields: `cards []Card`, `order []string`, `cursor int`, `width,height int`, `viewport viewport.Model` (glamour-rendered work-log), `prompting bool`, `prompt textinput.Model`, `pendingVerb Verb`, `macro Macro`, `stateRoot string`, `status string`, `confirming bool` (huh), `help bool`. `Init` renders the first card. `Update`:
- `tea.WindowSizeMsg` -> set width/height, re-lay regions, re-render glamour at width.
- when `prompting`: route keys to the textinput; Enter runs the verb with the typed arg (internal tier shells out via `macro.Exec`, then reloads the card from cache; completion tier flips to `confirming`; outward tier appends to `queue`), Esc cancels.
- when `confirming`: y/n; y runs the completion macro.
- otherwise a `tea.KeyMsg` resolves via `VerbForKey`. Nav verbs act immediately (next/back move cursor, more toggles the viewport height, open runs xdg-open, quit returns `tea.Quit`, ? toggles help). Internal/completion verbs with `NeedsArg` open the prompt; zero-arg internal verbs run immediately.
- persist the session (cursor+scope) on every card transition.

`View` composes header (lipgloss), body (viewport), footer (freshness lines), and the action line (the key legend) or the prompt or the confirm or the help overlay. Keep `View` a pure function of state.

Rendering the work-log: build a Markdown document from the task description and each comment (with its posted time and any attachment name), render once through `glamour.NewTermRenderer(glamour.WithWordWrap(width))`, and feed the result to the viewport. Re-render on resize.

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/tui/`.

- [ ] **Step 5: Commit** — `git commit -m "feat(tui): bubbletea model, layout, and keyword update loop"`.

---

## Task 10: CLI wiring and the run entrypoint

**Files:** Create `internal/tui/run.go`, `internal/cli/walk.go`; modify `internal/cli/cli.go`, `internal/cli/cli_test.go`.

- [ ] **Step 1: Failing test** — `Run([]string{"--task"}, ...)` (bare verb path) with no cache reports a clear "nothing to walk" style error rather than `ErrNotImplemented`. Because a real bubbletea program needs a TTY, gate the actual program launch behind a resolved-deps function that the test can exercise up to the point of launch. Test `runWalk`'s pre-launch resolution: an empty cache returns a sentinel `errEmptyCache`; a populated temp cache resolves N cards.

```go
func TestWalkEmptyCache(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveWalk(walkFlags{stateDir: dir, scope: Scope{Kind: ScopeAll}})
	if !errors.Is(err, errEmptyCache) {
		t.Fatalf("empty cache error = %v, want errEmptyCache", err)
	}
}

func TestWalkResolvesCards(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveTask(sources.Task{ID: "1", Title: "one", Project: "Kong"})
	got, err := resolveWalk(walkFlags{stateDir: dir, scope: Scope{Kind: ScopeProject, Value: "Kong"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(got.cards))
	}
}
```

- [ ] **Step 2: Verify fail.**

- [ ] **Step 3: Implement** `internal/tui/run.go` with `resolveWalk(walkFlags) (walkData, error)` (opens the store, loads all tasks + report + watermark, resolves scope, builds and sorts cards, errors `errEmptyCache` when none) and `Run(walkData) error` that builds the Model and runs `tea.NewProgram(...).Run()`. In `internal/cli/walk.go`, add `parseWalkFlags` (`--project`, `--filter`, `--preset`, `--task`, `--scripts-dir`, `--refresh`) rejecting more than one scope flag, and a `runWalk` that calls `resolveWalk`; on `errEmptyCache` (or `--refresh`) it shells out to `ballpoint probe` once, then retries; on success it launches the picker when no scope flag was given (huh), then `tui.Run`. In `cli.go`, replace the `case ""` `ErrNotImplemented` return with `return runWalk(rest[1:], stdout, stderr)`. Keep the stray-argument guard for a non-flag trailing token.

Update `TestRunNotImplemented` in `cli_test.go`: the bare invocation is no longer `ErrNotImplemented`. Move the bare case out of that test (only `dispatch` remains not-implemented) and add a bare-walk case that, with no cache and no TTY, returns a non-nil error that is not `ErrNotImplemented`.

- [ ] **Step 4: Verify pass** — `nix develop --command go test ./internal/cli/ ./internal/tui/`.

- [ ] **Step 5: Update usage** in `cli.go` (`ballpoint  walk the triage queue`) and regenerate the golden with `BALLPOINT_UPDATE_GOLDEN=1`.

- [ ] **Step 6: Commit** — `git commit -m "feat(cli): launch the triage-walk TUI on the bare command"`.

---

## Task 11: Full verification

- [ ] **Step 1:** `nix develop --command go test ./...` (all green).
- [ ] **Step 2:** `nix develop --command golangci-lint run` (0 issues).
- [ ] **Step 3:** `nix flake check` (all pass).
- [ ] **Step 4:** `nix run nixpkgs#nixpkgs-fmt -- --check flake.nix nix/*.nix` if any nix changed (none expected).
- [ ] **Step 5:** Manual smoke note in the PR body: the TUI itself needs a TTY and a real cache, so automated tests cover the model via `Update` and the pure core directly; a human smoke test (`ballpoint probe` then `ballpoint`) confirms the live terminal path.

---

## Self-Review

- **Acceptance criteria coverage:** bare launch (Task 10); scope project/filter/preset/task plus picker (Task 5, 10); render from cache, no network in the walk (Task 1, 2, 10, offline by construction); Markdown work-logs via glamour (Task 9); per-link freshness with `unchecked` (Task 4); moved-first, nothing hidden (Task 4 `SortMovedFirst`); internal tier immediate on keypress (Task 6, 9); completion confirm (Task 9 huh); outward queued never sent (Task 3, 9); help overlay (Task 9); resumable (Task 8, 9); resize (Task 9). SSH-friendliness is structural (Task 9/10 take io, no direct TTY reads).
- **Placeholder scan:** the `Run2`/`Exec` naming note in Task 7 and the outward-queue test body in Task 9 are the two spots to finalize during implementation; every other step carries concrete code.
- **Type consistency:** `Card`, `LinkLine`, `LinkState`, `Scope`/`ScopeKind`, `Verb`/`Tier`, `Macro`, `Session`, `walkData`/`walkFlags` are used consistently across tasks. `probe.Report`/`TaskReport`/`LinkFreshness` and `sources.Task` are the real upstream types.
