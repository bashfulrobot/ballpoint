# Todoist Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give ballpoint a `Source` interface and a direct Todoist HTTP client that replaces shelling out to `td`, plus the watermark and cache store the freshness probe (#3) reads.

**Architecture:** Four packages with one job each. `internal/secrets` reads the token from the off-store secrets file at runtime. `internal/sources` holds the interface and the normalised value types. `internal/store` persists watermarks and cached payloads under the state directory with atomic writes. `internal/sources/todoist` is the HTTP client: cursor pagination, name resolution, and per-task comment fetches under a bounded concurrency group. A mock `httptest.Server` benchmark proves the concurrency speedup in CI; a documented command produces the live figure.

**Tech Stack:** Go (`net/http`, `encoding/json`, `context`, `errgroup`, `golang.org/x/time/rate`, `httptest`), the existing `internal/config`, `internal/buildinfo`, `internal/golden`.

---

## Sequencing

`secrets` and the `sources` types have no internal dependencies and come
first. `store` and `todoist` both import `sources`. The benchmark and the
`probe` wiring import everything, so they come last. The `vendorHash` in
`nix/ballpoint.nix` is recomputed once at the end, after every dependency is in
`go.mod`.

`golang.org/x/sync/errgroup` and `golang.org/x/time/rate` are already pinned by
`internal/tools/tools.go` from issue #1, so importing them for real does not
change their versions.

**Secret discipline for every task below.** The token value must never be
printed, logged, put in an error message, or written to a golden file. Tests
use the literal string `test-token` so no real value is ever involved. Error
messages name the file path or the key, never the value.

## File structure

| File | Responsibility |
| --- | --- |
| `internal/secrets/secrets.go` | Load a flat key from the off-store JSON secrets file; resolve the default path. |
| `internal/sources/sources.go` | `Source` interface, `Watermark`, `Task`, `Comment`, `Delta`, priority normalisation. |
| `internal/store/store.go` | Watermark and cache persistence with atomic writes. |
| `internal/sources/todoist/client.go` | HTTP client: constructor, options, request helper. |
| `internal/sources/todoist/probe.go` | `Probe`: pagination, name resolution, bounded comment fetch, normalisation, delta assembly. |
| `internal/sources/todoist/wire.go` | Raw Todoist v1 JSON shapes and their conversion to `sources.Task`. |
| `internal/sources/todoist/*_test.go`, `testdata/` | httptest-based tests, golden files, the concurrency benchmark. |
| `internal/cli/cli.go` | Extend `probe` to accept `--benchmark` and document the live command. |
| `nix/ballpoint.nix`, `README.md` | vendorHash bump, usage and secret docs, recorded mock figure. |

---

### Task 1: Secret loader

**Files:**
- Create: `internal/secrets/secrets.go`, `internal/secrets/secrets_test.go`

- [ ] **Step 1: Write the failing test**

```go
package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecrets(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":"test-token","other":"x"}`)

	got, err := Load(path, "todoist_token")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != "test-token" {
		t.Errorf("Load() = %q, want %q", got, "test-token")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"), "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want a missing-file error")
	}
	if !strings.Contains(err.Error(), "secrets file") {
		t.Errorf("Load() error = %q, want it to mention the secrets file", err)
	}
}

func TestLoadMissingKey(t *testing.T) {
	path := writeSecrets(t, `{"other":"x"}`)

	_, err := Load(path, "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want a missing-key error")
	}
	if !strings.Contains(err.Error(), "todoist_token") {
		t.Errorf("Load() error = %q, want it to name the missing key", err)
	}
}

// A present but empty value is treated as missing, matching the aha script's
// `// empty` jq guard.
func TestLoadEmptyValue(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":""}`)

	_, err := Load(path, "todoist_token")
	if err == nil {
		t.Fatal("Load() error = nil, want an empty-value error")
	}
}

// The secret value must never appear in an error, even when the caller passes
// a wrong key and the file holds a real-looking token.
func TestLoadNeverLeaksValue(t *testing.T) {
	path := writeSecrets(t, `{"todoist_token":"super-secret-value"}`)

	_, err := Load(path, "absent_key")
	if err != nil && strings.Contains(err.Error(), "super-secret-value") {
		t.Errorf("Load() error leaked the secret value: %q", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/secrets/ -v`
Expected: FAIL, `undefined: Load`.

- [ ] **Step 3: Write the implementation**

```go
// Package secrets reads values from the off-store secrets file at runtime.
//
// The token cannot come from an environment variable or the Nix store: this
// binary runs under a systemd user timer (issue #4), and user services do not
// inherit session variables. The reference is
// modules/apps/cli/aha-fr-report/default.nix in the nixerator repo, which
// reads its token from the same file inside the script for the same reason.
package secrets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPath returns the standard off-store secrets file,
// ~/.config/nixos-secrets/secrets.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "nixos-secrets", "secrets.json"), nil
}

// Load reads a flat top-level string key from the JSON secrets file at path.
// It returns a distinct error for a missing file and for a missing or empty
// key. The value is returned to the caller and never logged; no error message
// includes it.
func Load(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading secrets file %s: %w", path, err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parsing secrets file %s: %w", path, err)
	}

	raw, ok := doc[key]
	if !ok {
		return "", fmt.Errorf("secrets file %s has no key %q", path, key)
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("key %q in %s is not a string", key, path)
	}

	if value == "" {
		return "", fmt.Errorf("key %q in %s is empty", key, path)
	}

	return value, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/secrets/ -v`
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/
git commit -m "feat: add runtime secret loader for the off-store secrets file"
```

---

### Task 2: Source types and priority normalisation

**Files:**
- Create: `internal/sources/types.go`, `internal/sources/priority.go`, `internal/sources/priority_test.go`
- Modify: `internal/sources/doc.go` (leave the package comment, it already names #2)

- [ ] **Step 1: Write the failing priority test**

```go
package sources

import "testing"

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		name string
		api  int
		want string
	}{
		{name: "urgent maps to p1", api: 4, want: "p1"},
		{name: "high maps to p2", api: 3, want: "p2"},
		{name: "medium maps to p3", api: 2, want: "p3"},
		{name: "none maps to p4", api: 1, want: "p4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePriority(tt.api); got != tt.want {
				t.Errorf("NormalizePriority(%d) = %q, want %q", tt.api, got, tt.want)
			}
		})
	}
}

// The API only ever returns 1 through 4. Anything else is clamped into range
// rather than producing p0 or p5, so a caller never sees an impossible band.
func TestNormalizePriorityClamps(t *testing.T) {
	if got := NormalizePriority(0); got != "p4" {
		t.Errorf("NormalizePriority(0) = %q, want p4", got)
	}
	if got := NormalizePriority(9); got != "p1" {
		t.Errorf("NormalizePriority(9) = %q, want p1", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/sources/ -v`
Expected: FAIL, `undefined: NormalizePriority`.

- [ ] **Step 3: Write the types**

`internal/sources/types.go`:

```go
package sources

import "context"

import "time"

// Watermark records the last seen activity time per link key. A link key
// identifies one task's relationship to one external system; for Todoist it is
// "todoist:<taskID>".
type Watermark map[string]time.Time

// Comment is a normalised Todoist comment.
type Comment struct {
	ID         string
	Content    string
	PostedAt   time.Time
	Attachment string // file name, empty when none
}

// Task is the normalised shape every source returns, independent of any one
// API's field names.
type Task struct {
	ID          string
	Title       string
	Project     string // resolved name, not the raw id
	Section     string // resolved name, empty when none
	Due         string // date or natural language, empty when none
	Recurring   bool
	Priority    string // always p1 through p4, never the raw API integer
	Labels      []string
	Description string
	URL         string
	UpdatedAt   time.Time
	Comments    []Comment
}

// Delta is what a probe returns: the tasks it fetched, the link keys whose
// activity moved past the incoming watermark, and the watermark to persist.
type Delta struct {
	Tasks   []Task
	Changed []string
	Next    Watermark
}

// Source is one external system. Adding a system means adding one package that
// implements this and nothing else.
type Source interface {
	Name() string
	Probe(ctx context.Context, since Watermark) (Delta, error)
}
```

`internal/sources/priority.go`:

```go
package sources

import "strconv"

// NormalizePriority converts Todoist's inverted API priority, where 4 is
// highest, into the p1 through p4 band the rest of ballpoint uses, where p1 is
// highest. Values outside 1 through 4 are clamped so a caller never sees an
// impossible band. Normalisation lives here so no caller ever handles the raw
// integer.
func NormalizePriority(api int) string {
	if api < 1 {
		api = 1
	}
	if api > 4 {
		api = 4
	}
	return "p" + strconv.Itoa(5-api)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/sources/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sources/
git commit -m "feat: add Source interface, normalised types, and priority mapping"
```

---

### Task 3: Watermark and cache store

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `internal/store/doc.go` (leave the package comment)

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestWatermarkRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	want := sources.Watermark{
		"todoist:1": time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		"todoist:2": time.Date(2026, 7, 19, 8, 30, 0, 0, time.UTC),
	}

	if err := s.SaveWatermark(want); err != nil {
		t.Fatalf("SaveWatermark() error = %v", err)
	}

	got, err := s.LoadWatermark()
	if err != nil {
		t.Fatalf("LoadWatermark() error = %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("LoadWatermark() returned %d entries, want %d", len(got), len(want))
	}
	for k, wv := range want {
		if !got[k].Equal(wv) {
			t.Errorf("LoadWatermark()[%q] = %v, want %v", k, got[k], wv)
		}
	}
}

// A first run has no watermark file. That is not an error; it loads empty so
// the probe fetches everything.
func TestLoadWatermarkMissingIsEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	got, err := s.LoadWatermark()
	if err != nil {
		t.Fatalf("LoadWatermark() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadWatermark() = %v, want empty", got)
	}
}

func TestTaskRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	want := sources.Task{
		ID:       "42",
		Title:    "ship the client",
		Priority: "p1",
		Comments: []sources.Comment{{ID: "c1", Content: "note"}},
	}

	if err := s.SaveTask(want); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	got, ok, err := s.LoadTask("42")
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadTask() ok = false, want true")
	}
	if got.Title != want.Title || got.Priority != want.Priority || len(got.Comments) != 1 {
		t.Errorf("LoadTask() = %+v, want %+v", got, want)
	}
}

func TestLoadTaskMissing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	_, ok, err := s.LoadTask("nope")
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if ok {
		t.Error("LoadTask() ok = true for a missing task, want false")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/store/ -v`
Expected: FAIL, `undefined: Open`.

- [ ] **Step 3: Write the implementation**

```go
// Package store persists watermarks and cached task payloads under the
// ballpoint state directory.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Store roots ballpoint's cache and watermarks at a directory, normally
// config.StateDir().
type Store struct {
	root string
}

// Open ensures the store's directories exist and returns a Store rooted there.
func Open(root string) (*Store, error) {
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating cache directory %s: %w", cacheDir, err)
	}
	return &Store{root: root}, nil
}

func (s *Store) watermarkPath() string { return filepath.Join(s.root, "watermarks.json") }

func (s *Store) taskPath(id string) string {
	return filepath.Join(s.root, "cache", id+".json")
}

// LoadWatermark reads the watermark map. A missing file returns an empty map,
// so a first run fetches everything.
func (s *Store) LoadWatermark() (sources.Watermark, error) {
	data, err := os.ReadFile(s.watermarkPath())
	if errors.Is(err, os.ErrNotExist) {
		return sources.Watermark{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.watermarkPath(), err)
	}

	var w sources.Watermark
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", s.watermarkPath(), err)
	}
	if w == nil {
		w = sources.Watermark{}
	}
	return w, nil
}

// SaveWatermark writes the watermark map atomically.
func (s *Store) SaveWatermark(w sources.Watermark) error {
	return writeAtomic(s.watermarkPath(), w)
}

// LoadTask reads a cached task. The bool is false when the task is not cached.
func (s *Store) LoadTask(id string) (sources.Task, bool, error) {
	data, err := os.ReadFile(s.taskPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return sources.Task{}, false, nil
	}
	if err != nil {
		return sources.Task{}, false, fmt.Errorf("reading %s: %w", s.taskPath(id), err)
	}

	var t sources.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return sources.Task{}, false, fmt.Errorf("parsing %s: %w", s.taskPath(id), err)
	}
	return t, true, nil
}

// SaveTask writes a task to the cache atomically.
func (s *Store) SaveTask(t sources.Task) error {
	return writeAtomic(s.taskPath(t.ID), t)
}

// writeAtomic marshals v and writes it by creating a temp file in the target's
// directory and renaming over the target, so a killed process never leaves a
// torn file.
func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/store/ -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat: add watermark and cache store with atomic writes"
```

---

### Task 4: Todoist wire shapes and conversion

**Files:**
- Create: `internal/sources/todoist/wire.go`, `internal/sources/todoist/wire_test.go`

- [ ] **Step 1: Write the failing test**

```go
package todoist

import (
	"testing"
	"time"
)

func TestRawTaskConvert(t *testing.T) {
	raw := rawTask{
		ID:         "10",
		Content:    "ship it",
		ProjectID:  "p100",
		SectionID:  "s5",
		Priority:   4,
		Labels:     []string{"work"},
		Desc:       "the description",
		URL:        "https://todoist.com/showTask?id=10",
		AddedAt:    "2026-07-18T12:00:00Z",
		UpdatedAt:  "2026-07-20T09:00:00Z",
		Due:        &rawDue{Date: "2026-07-25", String: "Jul 25", IsRecurring: true},
	}

	projects := map[string]string{"p100": "Inbox"}
	sections := map[string]string{"s5": "Doing"}

	task := raw.toTask(projects, sections)

	if task.Priority != "p1" {
		t.Errorf("priority = %q, want p1", task.Priority)
	}
	if task.Project != "Inbox" {
		t.Errorf("project = %q, want Inbox", task.Project)
	}
	if task.Section != "Doing" {
		t.Errorf("section = %q, want Doing", task.Section)
	}
	if !task.Recurring {
		t.Error("recurring = false, want true")
	}
	if task.Due != "2026-07-25" {
		t.Errorf("due = %q, want 2026-07-25", task.Due)
	}
	want := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	if !task.UpdatedAt.Equal(want) {
		t.Errorf("updatedAt = %v, want %v", task.UpdatedAt, want)
	}
}

// A task never updated since creation falls back to added_at for its
// watermark time, so it is never zero.
func TestRawTaskWatermarkFallback(t *testing.T) {
	raw := rawTask{ID: "11", Content: "x", Priority: 1, AddedAt: "2026-07-18T12:00:00Z"}

	task := raw.toTask(nil, nil)

	want := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if !task.UpdatedAt.Equal(want) {
		t.Errorf("updatedAt = %v, want added_at fallback %v", task.UpdatedAt, want)
	}
}

// An unknown project id resolves to the raw id rather than an empty string, so
// a card is never mislabelled as project-less.
func TestRawTaskUnknownProject(t *testing.T) {
	raw := rawTask{ID: "12", Content: "x", Priority: 1, ProjectID: "ghost", AddedAt: "2026-07-18T12:00:00Z"}

	task := raw.toTask(map[string]string{}, nil)

	if task.Project != "ghost" {
		t.Errorf("project = %q, want the raw id ghost", task.Project)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/sources/todoist/ -v`
Expected: FAIL, `undefined: rawTask`.

- [ ] **Step 3: Write the wire shapes**

```go
package todoist

import (
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// rawDue is Todoist v1's due object.
type rawDue struct {
	Date        string `json:"date"`
	String      string `json:"string"`
	IsRecurring bool   `json:"is_recurring"`
}

// rawTask is the subset of Todoist v1's task object ballpoint decodes. Every
// field name the client depends on lives here, so a Todoist rename is a
// one-place fix. Field names are v1 snake_case.
type rawTask struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	ProjectID string   `json:"project_id"`
	SectionID string   `json:"section_id"`
	Priority  int      `json:"priority"`
	Labels    []string `json:"labels"`
	Desc      string   `json:"description"`
	URL       string   `json:"url"`
	AddedAt   string   `json:"added_at"`
	UpdatedAt string   `json:"updated_at"`
	Due       *rawDue  `json:"due"`
}

// rawComment is the subset of a Todoist v1 comment ballpoint decodes.
type rawComment struct {
	ID         string          `json:"id"`
	Content    string          `json:"content"`
	PostedAt   string          `json:"posted_at"`
	Attachment *rawAttachment  `json:"file_attachment"`
}

type rawAttachment struct {
	FileName string `json:"file_name"`
}

// rawNamed is the shape shared by projects and sections: an id and a name.
type rawNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// parseTime parses an RFC3339 timestamp, returning the zero time on empty or
// malformed input rather than an error, so one odd field never fails a fetch.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// toTask converts a raw task into the normalised shape, resolving project and
// section ids to names and mapping the inverted priority. An unknown id
// resolves to the raw id so a card is never mislabelled as having none. The
// watermark time is updated_at, falling back to added_at.
func (r rawTask) toTask(projects, sections map[string]string) sources.Task {
	project := r.ProjectID
	if name, ok := projects[r.ProjectID]; ok {
		project = name
	}

	section := ""
	if r.SectionID != "" {
		section = r.SectionID
		if name, ok := sections[r.SectionID]; ok {
			section = name
		}
	}

	updated := parseTime(r.UpdatedAt)
	if updated.IsZero() {
		updated = parseTime(r.AddedAt)
	}

	due, recurring := "", false
	if r.Due != nil {
		due = r.Due.Date
		if due == "" {
			due = r.Due.String
		}
		recurring = r.Due.IsRecurring
	}

	return sources.Task{
		ID:          r.ID,
		Title:       r.Content,
		Project:     project,
		Section:     section,
		Due:         due,
		Recurring:   recurring,
		Priority:    sources.NormalizePriority(r.Priority),
		Labels:      r.Labels,
		Description: r.Desc,
		URL:         r.URL,
		UpdatedAt:   updated,
	}
}

// toComment converts a raw comment into the normalised shape.
func (r rawComment) toComment() sources.Comment {
	attachment := ""
	if r.Attachment != nil {
		attachment = r.Attachment.FileName
	}
	return sources.Comment{
		ID:         r.ID,
		Content:    r.Content,
		PostedAt:   parseTime(r.PostedAt),
		Attachment: attachment,
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/sources/todoist/ -v`
Expected: PASS, three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sources/todoist/wire.go internal/sources/todoist/wire_test.go
git commit -m "feat: add Todoist v1 wire shapes and normalisation"
```

---

### Task 5: Client, options, and paginated GET

**Files:**
- Create: `internal/sources/todoist/client.go`, `internal/sources/todoist/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAllPaginates(t *testing.T) {
	// Two pages: the first returns a cursor, the second returns none.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("User-Agent is empty, want a ballpoint agent")
		}

		cursor := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		if cursor == "" {
			json.NewEncoder(w).Encode(map[string]any{
				"results":     []map[string]string{{"id": "1"}},
				"next_cursor": "PAGE2",
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results":     []map[string]string{{"id": "2"}},
			"next_cursor": nil,
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithVersion("9.9.9"))

	var out []rawNamed
	if err := c.getAll(context.Background(), "/projects", nil, &out); err != nil {
		t.Fatalf("getAll() error = %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("getAll() returned %d items, want 2 across both pages", len(out))
	}
	if out[0].ID != "1" || out[1].ID != "2" {
		t.Errorf("getAll() ids = %q,%q, want 1,2", out[0].ID, out[1].ID)
	}
}

func TestGetAllSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	var out []rawNamed
	err := c.getAll(context.Background(), "/projects", nil, &out)
	if err == nil {
		t.Fatal("getAll() error = nil, want a 401 error")
	}
}

// The default concurrency limit is 12 when no option overrides it.
func TestDefaultLimit(t *testing.T) {
	c := New("test-token")
	if c.limit != 12 {
		t.Errorf("default limit = %d, want 12", c.limit)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/sources/todoist/ -run TestGetAll -v`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Write the client**

```go
package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://api.todoist.com/api/v1"

// Client talks to the Todoist v1 API. Construct it with New.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	http      *http.Client
	limit     int
	limiter   *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base, used by tests to point at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithVersion sets the version reported in the User-Agent.
func WithVersion(v string) Option {
	return func(c *Client) {
		c.userAgent = "ballpoint/" + v + " (+https://github.com/bashfulrobot/ballpoint)"
	}
}

// WithConcurrency sets the bounded fetch limit. Values below 1 are ignored.
func WithConcurrency(n int) Option {
	return func(c *Client) {
		if n >= 1 {
			c.limit = n
		}
	}
}

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Client. Concurrency defaults to 12 and the rate limiter to 50
// requests per second, Todoist's documented ceiling, so a large scope cannot
// burst past it.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL:   defaultBaseURL,
		token:     token,
		userAgent: "ballpoint/dev (+https://github.com/bashfulrobot/ballpoint)",
		http:      &http.Client{Timeout: 30 * time.Second},
		limit:     12,
		limiter:   rate.NewLimiter(rate.Limit(50), 50),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name identifies this source.
func (c *Client) Name() string { return "todoist" }

// page is the envelope every list endpoint returns.
type page struct {
	Results    json.RawMessage `json:"results"`
	NextCursor string          `json:"next_cursor"`
}

// getAll drains a paginated list endpoint into out, which must be a pointer to
// a slice, following next_cursor until it is empty. query holds any endpoint
// specific parameters; cursor and limit are added here.
func (c *Client) getAll(ctx context.Context, path string, query url.Values, out any) error {
	// Accumulate the raw result arrays, then unmarshal once into out.
	var buf []json.RawMessage

	cursor := ""
	for {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		q := url.Values{}
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		q.Set("limit", "200")
		if cursor != "" {
			q.Set("cursor", cursor)
		}

		var p page
		if err := c.get(ctx, path, q, &p); err != nil {
			return err
		}

		var items []json.RawMessage
		if err := json.Unmarshal(p.Results, &items); err != nil {
			return fmt.Errorf("decoding %s results: %w", path, err)
		}
		buf = append(buf, items...)

		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	combined, err := json.Marshal(buf)
	if err != nil {
		return fmt.Errorf("recombining %s results: %w", path, err)
	}
	if err := json.Unmarshal(combined, out); err != nil {
		return fmt.Errorf("decoding %s into target: %w", path, err)
	}
	return nil
}

// get performs one GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The token is in a header, never the URL, so naming the endpoint and
		// status leaks nothing.
		return fmt.Errorf("todoist %s returned %s", path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s response: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/sources/todoist/ -run 'TestGetAll|TestDefaultLimit' -v`
Expected: PASS.

- [ ] **Step 5: Confirm the token never appears in a getAll error**

Run: `nix develop --command sh -c 'grep -n "c.token" internal/sources/todoist/client.go'`
Expected: exactly one hit, the `Authorization` header line. No error string interpolates the token.

- [ ] **Step 6: Commit**

```bash
git add internal/sources/todoist/client.go internal/sources/todoist/client_test.go
git commit -m "feat: add Todoist client with cursor pagination and rate limiting"
```

---

### Task 6: Probe, bounded comment fetch, golden file

**Files:**
- Create: `internal/sources/todoist/probe.go`, `internal/sources/todoist/probe_test.go`, `internal/sources/todoist/testdata/probe.golden`

- [ ] **Step 1: Write the failing test**

```go
package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// fakeTodoist serves a small fixed scope: two tasks, one comment each, one
// project, one section. It records max in-flight comment requests so a test
// can assert the fetch is concurrent.
type fakeTodoist struct {
	inFlight   int32
	maxInFlight int32
}

func (f *fakeTodoist) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks":
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "1", "content": "first", "project_id": "p1", "priority": 4,
						"added_at": "2026-07-18T12:00:00Z", "updated_at": "2026-07-20T09:00:00Z"},
					{"id": "2", "content": "second", "project_id": "p1", "section_id": "s1", "priority": 1,
						"added_at": "2026-07-17T12:00:00Z", "updated_at": "2026-07-19T08:00:00Z"},
				},
				"next_cursor": nil,
			})
		case "/projects":
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "p1", "name": "Inbox"}}, "next_cursor": nil,
			})
		case "/sections":
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "s1", "name": "Doing"}}, "next_cursor": nil,
			})
		case "/comments":
			n := atomic.AddInt32(&f.inFlight, 1)
			for {
				old := atomic.LoadInt32(&f.maxInFlight)
				if n <= old || atomic.CompareAndSwapInt32(&f.maxInFlight, old, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&f.inFlight, -1)

			id := r.URL.Query().Get("task_id")
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "c" + id, "content": "comment for " + id, "posted_at": "2026-07-20T10:00:00Z"},
				},
				"next_cursor": nil,
			})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	})
}

func TestProbeFetchesAndNormalises(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithVersion("9.9.9"))

	delta, err := c.Probe(context.Background(), sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if len(delta.Tasks) != 2 {
		t.Fatalf("Probe() returned %d tasks, want 2", len(delta.Tasks))
	}

	// Stable order for the golden file: sort by ID.
	byID := map[string]sources.Task{}
	for _, task := range delta.Tasks {
		byID[task.ID] = task
	}
	if byID["1"].Priority != "p1" {
		t.Errorf("task 1 priority = %q, want p1", byID["1"].Priority)
	}
	if byID["1"].Project != "Inbox" {
		t.Errorf("task 1 project = %q, want Inbox", byID["1"].Project)
	}
	if byID["2"].Section != "Doing" {
		t.Errorf("task 2 section = %q, want Doing", byID["2"].Section)
	}
	if len(byID["1"].Comments) != 1 || byID["1"].Comments[0].Content != "comment for 1" {
		t.Errorf("task 1 comments = %+v, want one comment for 1", byID["1"].Comments)
	}

	rendered, err := json.MarshalIndent(delta, "", "  ")
	if err != nil {
		t.Fatalf("marshalling delta: %v", err)
	}
	golden.Assert(t, "probe.golden", string(rendered))
}

// Both comment fetches must overlap, proving the bounded group runs them
// concurrently rather than one after another.
func TestProbeFetchesCommentsConcurrently(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithConcurrency(12))

	if _, err := c.Probe(context.Background(), sources.Watermark{}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if atomic.LoadInt32(&fake.maxInFlight) < 2 {
		t.Errorf("max in-flight comment requests = %d, want at least 2 (concurrent)", fake.maxInFlight)
	}
}

// Changed holds a link key whose updated_at is after the incoming watermark;
// an up-to-date task is absent from Changed.
func TestProbeComputesChanged(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	since := sources.Watermark{
		"todoist:1": time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC), // equal to task 1, so unchanged
		// task 2 absent, so it counts as changed
	}

	delta, err := c.Probe(context.Background(), since)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	changed := map[string]bool{}
	for _, k := range delta.Changed {
		changed[k] = true
	}
	if changed["todoist:1"] {
		t.Error("todoist:1 in Changed, want absent (watermark equal to updated_at)")
	}
	if !changed["todoist:2"] {
		t.Error("todoist:2 not in Changed, want present (absent from watermark)")
	}
	if !delta.Next["todoist:1"].Equal(time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("Next[todoist:1] = %v, want the task's updated_at", delta.Next["todoist:1"])
	}
}

var _ = fmt.Sprintf // keep fmt imported if unused after edits
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/sources/todoist/ -run TestProbe -v`
Expected: FAIL, `c.Probe undefined`.

- [ ] **Step 3: Write the probe**

```go
package todoist

import (
	"context"
	"net/url"
	"sort"

	"golang.org/x/sync/errgroup"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// linkKey is the watermark key for a Todoist task.
func linkKey(taskID string) string { return "todoist:" + taskID }

// Probe fetches the whole scope: the task list, the project and section name
// maps, and every task's comments under a bounded concurrency group. It
// normalises the result and computes the delta against since.
func (c *Client) Probe(ctx context.Context, since sources.Watermark) (sources.Delta, error) {
	var rawTasks []rawTask
	if err := c.getAll(ctx, "/tasks", nil, &rawTasks); err != nil {
		return sources.Delta{}, err
	}

	var rawProjects []rawNamed
	if err := c.getAll(ctx, "/projects", nil, &rawProjects); err != nil {
		return sources.Delta{}, err
	}
	var rawSections []rawNamed
	if err := c.getAll(ctx, "/sections", nil, &rawSections); err != nil {
		return sources.Delta{}, err
	}

	projects := nameMap(rawProjects)
	sections := nameMap(rawSections)

	tasks := make([]sources.Task, len(rawTasks))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(c.limit)

	for i := range rawTasks {
		i := i
		group.Go(func() error {
			task := rawTasks[i].toTask(projects, sections)

			q := url.Values{}
			q.Set("task_id", task.ID)
			var rawComments []rawComment
			if err := c.getAll(gctx, "/comments", q, &rawComments); err != nil {
				return err
			}

			comments := make([]sources.Comment, len(rawComments))
			for j, rc := range rawComments {
				comments[j] = rc.toComment()
			}
			task.Comments = comments

			tasks[i] = task
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return sources.Delta{}, err
	}

	next := sources.Watermark{}
	var changed []string
	for _, task := range tasks {
		key := linkKey(task.ID)
		next[key] = task.UpdatedAt

		prev, ok := since[key]
		if !ok || task.UpdatedAt.After(prev) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)

	return sources.Delta{Tasks: tasks, Changed: changed, Next: next}, nil
}

// nameMap turns a list of id/name pairs into an id to name lookup.
func nameMap(items []rawNamed) map[string]string {
	m := make(map[string]string, len(items))
	for _, it := range items {
		m[it.ID] = it.Name
	}
	return m
}
```

- [ ] **Step 4: Generate the golden file, then verify**

Run: `nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./internal/sources/todoist/ -run TestProbeFetchesAndNormalises'`
Then: `nix develop --command go test ./internal/sources/todoist/ -run TestProbe -v`
Expected: PASS. Open `internal/sources/todoist/testdata/probe.golden` and confirm it holds two normalised tasks with `p1`/`p4`, resolved names, and one comment each.

- [ ] **Step 5: Remove the unused fmt guard if the linter flags it**

Run: `nix develop --command golangci-lint run ./internal/sources/todoist/`
If it flags the `var _ = fmt.Sprintf` line or the `fmt` import in the test, delete both. Re-run to confirm clean.

- [ ] **Step 6: Commit**

```bash
git add internal/sources/todoist/probe.go internal/sources/todoist/probe_test.go internal/sources/todoist/testdata/
git commit -m "feat: add Todoist probe with bounded concurrent comment fetch"
```

---

### Task 7: Concurrency benchmark

**Files:**
- Create: `internal/sources/todoist/bench_test.go`

- [ ] **Step 1: Write the benchmark as a test that asserts the speedup**

This is a test, not a `Benchmark` function, so it runs in CI and fails if the
concurrency ever regresses. It builds a 71 task scope against a mock server
that sleeps per request, fetches once at limit 1 and once at 12, and asserts
the concurrent run is at least four times faster. It prints both wall clock
figures for the PR.

```go
package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// scopeServer serves n tasks and sleeps perCall on every request, standing in
// for real API latency without a token or the network.
func scopeServer(n int, perCall time.Duration) http.Handler {
	tasks := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%d", i+1)
		tasks[i] = map[string]any{
			"id": id, "content": "task " + id, "project_id": "p1", "priority": 1,
			"added_at": "2026-07-18T12:00:00Z", "updated_at": "2026-07-20T09:00:00Z",
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perCall)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks":
			json.NewEncoder(w).Encode(map[string]any{"results": tasks, "next_cursor": nil})
		case "/projects":
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "p1", "name": "Inbox"}}, "next_cursor": nil})
		case "/sections":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{}, "next_cursor": nil})
		case "/comments":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}, "next_cursor": nil})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	})
}

func fetchWall(t *testing.T, baseURL string, concurrency int) time.Duration {
	t.Helper()
	c := New("test-token", WithBaseURL(baseURL), WithConcurrency(concurrency))
	start := time.Now()
	delta, err := c.Probe(context.Background(), sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if len(delta.Tasks) != 71 {
		t.Fatalf("Probe() returned %d tasks, want 71", len(delta.Tasks))
	}
	return time.Since(start)
}

// TestConcurrencySpeedup proves bounded concurrency removes the sequential
// cost. With 71 tasks and a 10 ms per-call latency, the sequential comment
// fetch is ~710 ms while the 12-way fetch is ~60 ms. Requiring a 4x speedup
// leaves wide margin against CI scheduling noise.
func TestConcurrencySpeedup(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive, skipped under -short")
	}

	srv := httptest.NewServer(scopeServer(71, 10*time.Millisecond))
	defer srv.Close()

	seq := fetchWall(t, srv.URL, 1)
	conc := fetchWall(t, srv.URL, 12)

	t.Logf("71 task fetch: sequential %v, 12-way %v, speedup %.1fx", seq, conc, float64(seq)/float64(conc))

	if conc*4 > seq {
		t.Errorf("12-way fetch %v not 4x faster than sequential %v", conc, seq)
	}
}
```

- [ ] **Step 2: Run it and capture the printed figure**

Run: `nix develop --command go test ./internal/sources/todoist/ -run TestConcurrencySpeedup -v`
Expected: PASS, with a log line like `71 task fetch: sequential 730ms, 12-way 70ms, speedup 10.4x`. Record the printed numbers; they go in the PR body and the README.

- [ ] **Step 3: Commit**

```bash
git add internal/sources/todoist/bench_test.go
git commit -m "test: prove bounded concurrency removes the sequential fetch cost"
```

---

### Task 8: Wire the benchmark command into the CLI

**Files:**
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/cli_test.go`, `internal/cli/testdata/usage.golden`

The `probe` verb is still #3's to implement. This task only teaches the CLI to
recognise `--benchmark` so the live command in the README resolves to real
flag handling rather than a promise, and so `probe` still exits non-zero as
not-implemented.

- [ ] **Step 1: Write the failing test**

```go
// Added to cli_test.go. probe --benchmark is still not implemented, but it
// must parse as a known flag rather than an unknown-argument error, so the
// documented live command is real.
func TestRunProbeBenchmarkParses(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"probe", "--benchmark"}, &stdout, &stderr)

	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Run(probe --benchmark) error = %v, want ErrNotImplemented", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `nix develop --command go test ./internal/cli/ -run TestRunProbeBenchmark -v`
Expected: FAIL. The current code rejects any argument after `probe`, so this returns a stray-argument error, not `ErrNotImplemented`.

- [ ] **Step 3: Teach the probe case to accept its own flags**

Replace the `case "probe", "dispatch":` arm in `Run` so `probe` parses a
subcommand FlagSet while `dispatch` keeps the strict no-argument check. This is
the "per-verb FlagSet arrives with the first verb that takes a flag" point from
issue #1 coming due.

```go
	switch cmd {
	case "":
		return fmt.Errorf("triage walk: %w", ErrNotImplemented)
	case "probe":
		pf := flag.NewFlagSet("probe", flag.ContinueOnError)
		pf.SetOutput(stderr)
		pf.Bool("benchmark", false, "time a full prefetch against the live API and print the wall clock")
		if err := pf.Parse(rest[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if pf.NArg() > 0 {
			return fmt.Errorf("probe takes no positional arguments, got %q", pf.Args())
		}
		return fmt.Errorf("probe: %w", ErrNotImplemented)
	case "dispatch":
		if len(rest) > 1 {
			return fmt.Errorf("dispatch takes no arguments, got %q", rest[1:])
		}
		return fmt.Errorf("dispatch: %w", ErrNotImplemented)
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
```

Remove the now-unused shared `len(rest) > 1` guard that previously sat before
the switch, since each case now owns its own argument handling. Keep the
`--version` stray-argument check as it was.

- [ ] **Step 4: Update the usage text and its golden file**

Change the `usage` const so the `probe` line documents the flag:

```
  ballpoint probe [--benchmark]   refresh freshness data
```

Then regenerate the golden file.

Run: `nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestRunHelp'`

- [ ] **Step 5: Run the CLI tests**

Run: `nix develop --command go test ./internal/cli/ -v`
Expected: PASS, including the new `TestRunProbeBenchmarkParses`, the existing stray-argument cases (`dispatch extra` still rejected, `probe --nope` now rejected by the FlagSet as an unknown flag), and the mistyped-verb case.

- [ ] **Step 6: Verify probe --nope is still rejected**

Run: `nix develop --command sh -c 'go build ./... && ./ballpoint probe --nope; echo exit=$?' 2>&1 | tail -2` (or `go run ./cmd/ballpoint`)
Expected: an unknown-flag error from the FlagSet and a non-zero exit. If `ballpoint` is not on PATH, use `go run ./cmd/ballpoint probe --nope`.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat: parse probe --benchmark so the documented live command is real"
```

---

### Task 9: Recompute vendorHash and build

**Files:**
- Modify: `nix/ballpoint.nix`

`errgroup` and `rate` moved from tools-tagged blank imports to real imports, so
they are now in the build graph and the vendor hash changes.

- [ ] **Step 1: Tidy and confirm the new deps are direct**

Run: `nix develop --command go mod tidy`
Then: `nix develop --command sh -c 'grep -E "golang.org/x/(sync|time)" go.mod'`
Expected: both appear, no longer only behind the tools tag.

- [ ] **Step 2: Set the vendorHash to the sentinel and read the real one**

In `nix/ballpoint.nix`, set `vendorHash = lib.fakeHash;`, then:

Run: `nix build 2>&1 | tee /tmp/ballpoint-vendor.log | grep -E "got:"`
Expected: a `got: sha256-...` line. Copy that value over `lib.fakeHash`.

- [ ] **Step 3: Build and run the whole verification set**

```bash
nix build
nix develop --command go test ./...
nix develop --command golangci-lint run
nix flake check
```

Expected: all exit 0. `nix build` still stamps `0.1.0`.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum nix/ballpoint.nix
git commit -m "build: recompute vendorHash for errgroup and rate dependencies"
```

---

### Task 10: README and secret documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the secret, the benchmark figures, and the sources layout**

Add a section covering:

- The off-store secret. ballpoint reads the Todoist token from
  `~/.config/nixos-secrets/secrets.json` at the flat key `todoist_token`, at
  runtime, never from an environment variable or the Nix store, because the
  timer in issue #4 cannot see session variables. Point at
  `modules/apps/cli/aha-fr-report/default.nix` in nixerator as the pattern.
- The benchmark. Record the mock figure from Task 7 Step 2 (the printed
  sequential, 12-way, and speedup numbers) and state plainly that it proves the
  concurrency mechanism against a mock server, not the live API. Then document
  the live command: `ballpoint probe --benchmark` loads the token the normal
  way, runs one real prefetch, and prints the wall clock against the 15.3 s
  `td` baseline. Note the command is wired here and filled in by #3.
- The sources layout. One package per external system under
  `internal/sources`; adding a system is adding one package that implements
  `sources.Source`.

Use the real numbers from Task 7, not placeholders.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document the off-store secret, the benchmark, and the sources layout"
```

---

### Task 11: Full acceptance verification

- [ ] **Step 1: Run every command the issue names**

```bash
nix develop --command go test ./...
nix develop --command golangci-lint run
```

Expected: all pass.

- [ ] **Step 2: Confirm each acceptance criterion**

```bash
# Source interface with the right signature
nix develop --command go doc ./internal/sources Source
# Todoist client, no td dependency
nix develop --command sh -c 'grep -rn "\btd\b" internal/sources/todoist/ || echo "no td binary reference"'
# Priority normalised, no raw integer escapes
nix develop --command go doc ./internal/sources NormalizePriority
# Store keyed by task and by link
nix develop --command sh -c 'ls internal/store/ && go doc ./internal/store Store'
# Default concurrency 12
nix develop --command go test ./internal/sources/todoist/ -run TestDefaultLimit -v
# Benchmark beats baseline
nix develop --command go test ./internal/sources/todoist/ -run TestConcurrencySpeedup -v
```

Expected: the Source interface has `Probe(ctx, Watermark) (Delta, error)`; no
`td` binary reference; the priority doc shows the p1 through p4 mapping; the
store exposes watermark and task methods; the default limit is 12; the speedup
test passes and logs the figure.

- [ ] **Step 3: Confirm the secret never leaks in any test output**

Run: `nix develop --command sh -c 'go test ./... -v 2>&1 | grep -i "super-secret\|todoist_token.*[A-Za-z0-9]\{20\}" || echo "no secret value in test output"'`
Expected: `no secret value in test output`. The only token literal anywhere is `test-token`.

---

## Self-review

**Spec coverage.** `internal/secrets`, Task 1. Source interface and types,
Task 2. Priority normalisation, Task 2. Store, Task 3. Wire shapes and
normalisation, Task 4. Client, pagination, rate limiting, User-Agent, Task 5.
Probe with bounded comment fetch and delta, Task 6. Benchmark, Task 7. The
`--benchmark` command seam, Task 8. vendorHash, Task 9. README with the real
figure and the secret docs, Task 10. Every acceptance criterion is checked in
Task 11. No gaps.

**Placeholder scan.** The only sentinel is `lib.fakeHash` in Task 9, which is
real Nix resolved in the same task. The benchmark figure is a real number
captured in Task 7 Step 2 and carried into Task 10, not a placeholder. The
`var _ = fmt.Sprintf` guard in the Task 6 test is removed in Step 5 if unused.

**Type consistency.** `sources.Watermark`, `sources.Task`, `sources.Comment`,
`sources.Delta`, and `sources.Source` are defined in Task 2 and used with the
same shapes in Tasks 3, 4, and 6. `sources.NormalizePriority(int) string` is
defined in Task 2 and called in Task 4. `rawTask.toTask(projects, sections)`
is defined in Task 4 and called in Task 6. `New`, `WithBaseURL`, `WithVersion`,
`WithConcurrency`, `getAll`, and `c.limit` are defined in Task 5 and used in
Tasks 6 and 7. `store.Open`, `LoadWatermark`, `SaveWatermark`, `LoadTask`,
`SaveTask` are defined in Task 3 with the signatures the spec's data flow uses.
`ErrNotImplemented` and `Run` come from issue #1 and are extended, not
redefined, in Task 8.

**Secret discipline.** No task prints, logs, or golden-files a token. Tests use
`test-token`; the leak checks in Task 1 and Task 11 assert no value escapes.
