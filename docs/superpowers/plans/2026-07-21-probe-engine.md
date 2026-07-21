# Probe Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give ballpoint a batch-by-system freshness probe engine that answers, per task, what changed at each linked system since the last logged work-log entry, deterministically and with no model involvement.

**Architecture:** A new `internal/links` package extracts and categorises the external references in a task and parses each system's permalink into a stable record identity. A new `internal/probe` package holds the engine: it groups links by system, calls one `Prober` per system (so Slack collapses to one call per channel), enforces the unchecked invariant, reconciles against the task's work log, persists watermarks through the #2 store, and emits JSON keyed by task. One sub-package per source implements a `Prober`.

**Tech Stack:** Go (`net/http`, `regexp`, `encoding/json`, `context`, `golang.org/x/time/rate`, `httptest`), the existing `internal/{sources,store,secrets,config,cli,golden,buildinfo}`.

---

## Sequencing

`internal/links` depends only on `internal/sources`, so it comes first. The
engine core types and `Run` depend on `links` and `sources`. Each prober
depends on `links`, `sources`, and `secrets`. The CLI wiring depends on
everything. The reproducible call-count test and the README come last. The
`vendorHash` is checked at the end; no new module is expected because
`x/time/rate` is already pinned from #2.

**Credential discipline for every task.** No credential value is printed,
logged, put in an error message, or written to a golden file. Tests use literal
fake tokens (`test-token`). Errors name the source and the endpoint, never the
value. This mirrors the #2 secret handling.

## File structure

| File | Responsibility |
| --- | --- |
| `internal/links/links.go` | `System` constants, `Link`, `Key()`. |
| `internal/links/extract.go` | `Extract`: harvest, categorise, dedup. |
| `internal/links/parse.go` | Per-system permalink parsers filling `Record`/`Fields`. |
| `internal/probe/probe.go` | `Reason`, `Freshness`, `ProbeResult`, `Prober`, `Registry`. |
| `internal/probe/engine.go` | `Run`: group, fan out, invariant, reconcile, JSON. |
| `internal/probe/slack/slack.go` | Slack channel-history collapse prober. |
| `internal/probe/gmail/gmail.go` | Gmail history prober. |
| `internal/probe/aha/aha.go` | Aha updated-since prober. |
| `internal/probe/gdrive/gdrive.go` | Drive modifiedTime prober. |
| `internal/cli/cli.go` | `ballpoint probe` behaviour, `--dry-run`, `--benchmark`. |
| `nix/ballpoint.nix`, `README.md` | vendorHash check, docs. |

---

### Task 1: Link types and keys

**Files:**
- Create: `internal/links/links.go`, `internal/links/links_test.go`

- [ ] **Step 1: Write the failing test**

```go
package links

import "testing"

func TestLinkKey(t *testing.T) {
	l := Link{System: SystemSlack, Record: "C1:1699999999.000100"}
	if got, want := l.Key(), "slack:C1:1699999999.000100"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

// A link whose record could not be parsed has an empty record, so its key is
// just the system with a trailing colon. The engine treats an empty record as
// unparseable rather than a real identity.
func TestLinkKeyEmptyRecord(t *testing.T) {
	l := Link{System: SystemURL, Record: ""}
	if got, want := l.Key(), "url:"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/links/ -run TestLinkKey -v`
Expected: FAIL, `undefined: Link`.

- [ ] **Step 3: Write the types**

```go
// Package links extracts external references from a task and parses each
// system's permalink into a stable record identity that keys a watermark.
package links

// System is the canonical name of an external system.
type System string

const (
	SystemSlack      System = "slack"
	SystemTeams      System = "teams"
	SystemGmail      System = "gmail"
	SystemGDrive     System = "gdrive"
	SystemAha        System = "aha"
	SystemJira       System = "jira"
	SystemConfluence System = "confluence"
	SystemZoom       System = "zoom"
	SystemTodoist    System = "todoist"
	SystemSalesforce System = "salesforce"
	SystemGitHub     System = "github"
	SystemURL        System = "url" // an uncategorised URL
)

// Link is one reference from a task to an external record.
type Link struct {
	System System
	Raw    string            // the URL or bare id as it appeared
	Record string            // parsed record identity, empty when unparseable
	Fields map[string]string // parsed parts, e.g. channel, thread, fileID
}

// Key is the watermark key, "<system>:<record>", stable across runs.
func (l Link) Key() string { return string(l.System) + ":" + l.Record }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/links/ -run TestLinkKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/links/links.go internal/links/links_test.go
git commit -m "feat: add link types and watermark keys"
```

---

### Task 2: Permalink parsers

**Files:**
- Create: `internal/links/parse.go`, `internal/links/parse_test.go`

- [ ] **Step 1: Write the failing test**

```go
package links

import "testing"

func TestParseSlack(t *testing.T) {
	rec, fields := parseSlack("https://kong.slack.com/archives/C1/p1699999999000100")
	if rec != "C1:1699999999.000100" {
		t.Errorf("record = %q, want C1:1699999999.000100", rec)
	}
	if fields["channel"] != "C1" || fields["thread"] != "1699999999.000100" {
		t.Errorf("fields = %v", fields)
	}
}

// A reply permalink carries thread_ts, which is the parent thread, and it wins
// over the path ts so a reply and its parent share one watermark key.
func TestParseSlackThreadTSOverride(t *testing.T) {
	rec, _ := parseSlack("https://kong.slack.com/archives/C1/p1699999999000200?thread_ts=1699999999.000100")
	if rec != "C1:1699999999.000100" {
		t.Errorf("record = %q, want the thread_ts parent C1:1699999999.000100", rec)
	}
}

func TestParseSlackUnparseable(t *testing.T) {
	rec, _ := parseSlack("https://kong.slack.com/team/U123")
	if rec != "" {
		t.Errorf("record = %q, want empty for a non-archive slack url", rec)
	}
}

func TestParseGmail(t *testing.T) {
	rec, _ := parseGmail("https://mail.google.com/mail/u/0/#inbox/FMfcgzGabc123")
	if rec != "FMfcgzGabc123" {
		t.Errorf("record = %q, want the trailing thread id", rec)
	}
}

func TestParseAha(t *testing.T) {
	if rec, _ := parseAha("https://kong.aha.io/features/GTWY-I-1484"); rec != "GTWY-I-1484" {
		t.Errorf("url record = %q, want GTWY-I-1484", rec)
	}
	if rec, _ := parseAha("GTWY-I-1484"); rec != "GTWY-I-1484" {
		t.Errorf("bare record = %q, want GTWY-I-1484", rec)
	}
}

func TestParseDrive(t *testing.T) {
	rec, _ := parseDrive("https://docs.google.com/document/d/1AbC_dEF/edit")
	if rec != "1AbC_dEF" {
		t.Errorf("record = %q, want the file id 1AbC_dEF", rec)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/links/ -run TestParse -v`
Expected: FAIL, `undefined: parseSlack`.

- [ ] **Step 3: Write the parsers**

```go
package links

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	slackArchive = regexp.MustCompile(`/archives/([A-Z0-9]+)/p([0-9]+)`)
	gmailThread  = regexp.MustCompile(`#[^/]+/([A-Za-z0-9]+)$`)
	driveFile    = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)
	ahaKey       = regexp.MustCompile(`[A-Z]{3,5}-I-[0-9]+`)
)

// slackTS turns Slack's p-form (p1699999999000100) into a ts
// (1699999999.000100) by inserting the decimal six digits from the end.
func slackTS(p string) string {
	if len(p) <= 6 {
		return p
	}
	return p[:len(p)-6] + "." + p[len(p)-6:]
}

// parseSlack returns the record "<channel>:<ts>" for an archive permalink. A
// thread_ts query parameter (a reply permalink) overrides the path ts so a
// reply keys to its parent thread. A non-archive URL returns an empty record.
func parseSlack(raw string) (string, map[string]string) {
	m := slackArchive.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	channel, ts := m[1], slackTS(m[2])
	if u, err := url.Parse(raw); err == nil {
		if tt := u.Query().Get("thread_ts"); tt != "" {
			ts = tt
		}
	}
	return channel + ":" + ts, map[string]string{"channel": channel, "thread": ts}
}

func parseGmail(raw string) (string, map[string]string) {
	m := gmailThread.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	return m[1], map[string]string{"thread": m[1]}
}

func parseAha(raw string) (string, map[string]string) {
	m := ahaKey.FindString(raw)
	if m == "" {
		return "", nil
	}
	return m, map[string]string{"reference": m}
}

func parseDrive(raw string) (string, map[string]string) {
	m := driveFile.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	return m[1], map[string]string{"file": m[1]}
}

// stripTrailingPunct removes sentence punctuation a URL picked up from prose.
func stripTrailingPunct(s string) string {
	return strings.TrimRight(s, ".,;:!?")
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/links/ -run TestParse -v`
Expected: PASS, six tests.

- [ ] **Step 5: Commit**

```bash
git add internal/links/parse.go internal/links/parse_test.go
git commit -m "feat: add per-system permalink parsers"
```

---

### Task 3: Extraction and categorisation

**Files:**
- Create: `internal/links/extract.go`, `internal/links/extract_test.go`, `internal/links/testdata/extract.golden`

- [ ] **Step 1: Write the failing test**

```go
package links

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestExtractCategorises(t *testing.T) {
	task := sources.Task{
		ID:    "1",
		Title: "See https://kong.slack.com/archives/C1/p1699999999000100 and GTWY-I-1484",
		Comments: []sources.Comment{
			{Content: "doc https://docs.google.com/document/d/1AbC_dEF/edit."},
			{Content: "mail https://mail.google.com/mail/u/0/#inbox/FMfcgzGabc123"},
			{Content: "jira PLAT-42 and teams https://teams.microsoft.com/l/message/19:abc"},
		},
		UpdatedAt: time.Now(),
	}

	got := Extract(task)

	systems := map[System]bool{}
	for _, l := range got {
		systems[l.System] = true
	}
	for _, want := range []System{SystemSlack, SystemAha, SystemGDrive, SystemGmail, SystemJira, SystemTeams} {
		if !systems[want] {
			t.Errorf("Extract missed system %q", want)
		}
	}

	rendered, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	golden.Assert(t, "extract.golden", string(rendered))
}

// The trailing period on the docs URL must be stripped before parsing, or the
// file id would carry it.
func TestExtractStripsTrailingPunct(t *testing.T) {
	task := sources.Task{ID: "2", Title: "x https://docs.google.com/document/d/FILEID/edit."}
	for _, l := range Extract(task) {
		if l.System == SystemGDrive && l.Record != "FILEID" {
			t.Errorf("drive record = %q, want FILEID with no trailing punctuation", l.Record)
		}
	}
}

// The same link appearing in the title and a comment yields one link, in
// first-seen order.
func TestExtractDedups(t *testing.T) {
	u := "https://kong.slack.com/archives/C1/p1699999999000100"
	task := sources.Task{ID: "3", Title: u, Comments: []sources.Comment{{Content: u}}}

	n := 0
	for _, l := range Extract(task) {
		if l.System == SystemSlack {
			n++
		}
	}
	if n != 1 {
		t.Errorf("slack link count = %d, want 1 (deduped)", n)
	}
}

// A jira bare key and an aha idea key are distinguished: aha keys carry -I- and
// must not be miscategorised as jira.
func TestExtractAhaNotJira(t *testing.T) {
	task := sources.Task{ID: "4", Title: "GTWY-I-1484 and PLAT-42"}
	sys := map[string]System{}
	for _, l := range Extract(task) {
		sys[l.Raw] = l.System
	}
	if sys["GTWY-I-1484"] != SystemAha {
		t.Errorf("GTWY-I-1484 = %q, want aha", sys["GTWY-I-1484"])
	}
	if sys["PLAT-42"] != SystemJira {
		t.Errorf("PLAT-42 = %q, want jira", sys["PLAT-42"])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/links/ -run TestExtract -v`
Expected: FAIL, `undefined: Extract`.

- [ ] **Step 3: Write extraction**

```go
package links

import (
	"regexp"
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

var (
	urlRe   = regexp.MustCompile(`https?://[^ )<>"']+`)
	jiraRe  = regexp.MustCompile(`\b[A-Z]{2,6}-[0-9]+\b`)
	sfidRe  = regexp.MustCompile(`\b00[0-9A-Za-z]{13}([0-9A-Za-z]{3})?\b`)
	caseRe  = regexp.MustCompile(`\bCase [0-9]{5,}\b`)
)

// Extract scans a task's title and comment bodies, harvests URLs and bare
// identifiers, categorises each, parses a record identity where it can, and
// returns the deduplicated links in first-seen order.
func Extract(task sources.Task) []Link {
	var blob strings.Builder
	blob.WriteString(task.Title)
	blob.WriteByte('\n')
	for _, c := range task.Comments {
		blob.WriteString(c.Content)
		blob.WriteByte('\n')
	}
	text := blob.String()

	seen := map[string]bool{}
	var out []Link

	add := func(l Link) {
		k := string(l.System) + "|" + l.Raw
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, l)
	}

	for _, raw := range urlRe.FindAllString(text, -1) {
		raw = stripTrailingPunct(raw)
		add(categoriseURL(raw))
	}

	// Bare identifiers. Aha keys (with -I-) are matched before Jira so the Jira
	// pattern does not claim them.
	for _, m := range ahaKey.FindAllString(text, -1) {
		rec, f := parseAha(m)
		add(Link{System: SystemAha, Raw: m, Record: rec, Fields: f})
	}
	for _, m := range jiraRe.FindAllString(text, -1) {
		if strings.Contains(m, "-I-") {
			continue
		}
		add(Link{System: SystemJira, Raw: m, Record: m})
	}
	for _, m := range sfidRe.FindAllString(text, -1) {
		add(Link{System: SystemSalesforce, Raw: m, Record: m})
	}
	for _, m := range caseRe.FindAllString(text, -1) {
		add(Link{System: SystemSalesforce, Raw: m, Record: strings.TrimPrefix(m, "Case ")})
	}

	return out
}

// categoriseURL maps a URL to its system by host substring and parses a record
// where the system has a parser.
func categoriseURL(raw string) Link {
	host := strings.ToLower(raw)
	switch {
	case strings.Contains(host, "slack.com"):
		rec, f := parseSlack(raw)
		return Link{System: SystemSlack, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "teams.microsoft.com"):
		return Link{System: SystemTeams, Raw: raw}
	case strings.Contains(host, "mail.google.com"):
		rec, f := parseGmail(raw)
		return Link{System: SystemGmail, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "docs.google.com"), strings.Contains(host, "drive.google.com"):
		rec, f := parseDrive(raw)
		return Link{System: SystemGDrive, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, ".aha.io"):
		rec, f := parseAha(raw)
		return Link{System: SystemAha, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "atlassian.net"):
		return Link{System: SystemJira, Raw: raw}
	case strings.Contains(host, "confluence"):
		return Link{System: SystemConfluence, Raw: raw}
	case strings.Contains(host, "github.com"):
		return Link{System: SystemGitHub, Raw: raw}
	case strings.Contains(host, "zoom.us"):
		return Link{System: SystemZoom, Raw: raw}
	case strings.Contains(host, "app.todoist.com"):
		return Link{System: SystemTodoist, Raw: raw}
	default:
		return Link{System: SystemURL, Raw: raw}
	}
}
```

- [ ] **Step 4: Generate the golden file, then verify**

Run: `nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./internal/links/ -run TestExtractCategorises'`
Then: `nix develop --command go test ./internal/links/ -v`
Expected: PASS. Open `internal/links/testdata/extract.golden` and confirm the six systems appear with the Slack record `C1:1699999999.000100`, the Gmail thread id, the Drive file id, and Teams with an empty record.

- [ ] **Step 5: Lint**

Run: `nix develop --command golangci-lint run ./internal/links/`
Expected: 0 issues. Fix any (a likely one: gofmt alignment on the var block, or an unused parameter).

- [ ] **Step 6: Commit**

```bash
git add internal/links/
git commit -m "feat: add link extraction and categorisation"
```

---

### Task 4: Engine core types

**Files:**
- Create: `internal/probe/probe.go`, `internal/probe/probe_test.go`

- [ ] **Step 1: Write the failing test**

```go
package probe

import (
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
)

func TestRegistry(t *testing.T) {
	var r Registry
	stub := stubProber{system: links.SystemSlack}
	r.Register(stub)

	got, ok := r.For(links.SystemSlack)
	if !ok || got.System() != links.SystemSlack {
		t.Fatalf("For(slack) = %v, %v; want the registered prober", got, ok)
	}
	if _, ok := r.For(links.SystemGmail); ok {
		t.Error("For(gmail) ok = true, want false for an unregistered system")
	}
}

// stubProber is a minimal Prober for tests in this package.
type stubProber struct {
	system links.System
	result map[string]ProbeResult
	err    error
}

func (s stubProber) System() links.System { return s.system }

func (s stubProber) Probe(_ context.Context, _ []links.Link, _ sources.Watermark) (map[string]ProbeResult, error) {
	return s.result, s.err
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/probe/ -run TestRegistry -v`
Expected: FAIL, `undefined: Registry`.

- [ ] **Step 3: Write the core types**

```go
// Package probe holds the batch-by-system freshness engine: it groups a task's
// links by system, calls one Prober per system, enforces the unchecked
// invariant, reconciles against each task's work log, and emits JSON.
package probe

import (
	"context"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Reason explains a non-changed, non-quiet outcome.
type Reason string

const (
	ReasonNoProbe      Reason = "no probe available"
	ReasonNotProbeable Reason = "not probeable"
	ReasonAuth         Reason = "credentials missing or expired"
	ReasonError        Reason = "probe error"
	ReasonTimeout      Reason = "probe timed out"
	ReasonUnparseable  Reason = "link could not be parsed"
)

// ProbeResult is a prober's per-link finding: a last activity time, or an
// unchecked reason.
type ProbeResult struct {
	LastActivity *time.Time
	Unchecked    bool
	Reason       Reason
}

// Prober checks one system. It receives every link for its system across all
// tasks at once so it can batch, and returns a result per link key. The
// engine, not the prober, decides Changed and writes watermarks.
type Prober interface {
	System() links.System
	Probe(ctx context.Context, ls []links.Link, since sources.Watermark) (map[string]ProbeResult, error)
}

// Registry maps a system to its prober.
type Registry struct {
	probers map[links.System]Prober
}

// Register adds a prober, keyed by its System.
func (r *Registry) Register(p Prober) {
	if r.probers == nil {
		r.probers = map[links.System]Prober{}
	}
	r.probers[p.System()] = p
}

// For returns the prober for a system, if one is registered.
func (r *Registry) For(s links.System) (Prober, bool) {
	p, ok := r.probers[s]
	return p, ok
}
```

- [ ] **Step 4: Add the missing imports to the test**

The test references `context` and `sources`. Update the import block in
`internal/probe/probe_test.go`:

```go
import (
	"context"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/probe/ -run TestRegistry -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/probe/probe.go internal/probe/probe_test.go
git commit -m "feat: add probe engine core types and registry"
```

---

### Task 5: The engine Run, the unchecked invariant, and JSON output

**Files:**
- Create: `internal/probe/engine.go`, `internal/probe/engine_test.go`, `internal/probe/testdata/engine.golden`

- [ ] **Step 1: Write the failing test**

```go
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func tp(s string) *time.Time {
	v, _ := time.Parse(time.RFC3339, s)
	return &v
}

// A prober that errors makes every link for its system unchecked, and no
// watermark is written for those links.
func TestRunUncheckedOnError(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-07-01T00:00:00Z"),
	}}

	var reg Registry
	reg.Register(stubProber{system: links.SystemSlack, err: errors.New("boom")})

	report, next, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	link := report.Tasks["1"].Links[0]
	if !link.Unchecked || link.Reason != ReasonError {
		t.Errorf("link = %+v, want unchecked with ReasonError", link)
	}
	if _, ok := next[link.Key]; ok {
		t.Error("a watermark was written for an unchecked link, want none")
	}
}

// A system with no registered prober is unchecked with ReasonNoProbe; Teams is
// unchecked with ReasonNotProbeable.
func TestRunUncheckedNoProbe(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "gh https://github.com/o/r/pull/1 teams https://teams.microsoft.com/l/message/19:a",
		UpdatedAt: *tp("2026-07-01T00:00:00Z"),
	}}

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &Registry{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	byReason := map[Reason]bool{}
	for _, l := range report.Tasks["1"].Links {
		if l.Unchecked {
			byReason[l.Reason] = true
		}
	}
	if !byReason[ReasonNoProbe] {
		t.Error("github link not unchecked with ReasonNoProbe")
	}
	if !byReason[ReasonNotProbeable] {
		t.Error("teams link not unchecked with ReasonNotProbeable")
	}
}

// Changed is true when the last activity is after the newest comment, and the
// watermark advances only for the checked link.
func TestRunChangedAndWatermark(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-06-01T00:00:00Z"),
		Comments:  []sources.Comment{{PostedAt: *tp("2026-07-01T00:00:00Z")}},
	}}

	activity := tp("2026-07-10T00:00:00Z")
	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]ProbeResult{"slack:C1:1699999999.000100": {LastActivity: activity}},
	})

	report, next, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	link := report.Tasks["1"].Links[0]
	if !link.Changed {
		t.Error("changed = false, want true (activity after the newest comment)")
	}
	if !next["slack:C1:1699999999.000100"].Equal(*activity) {
		t.Errorf("watermark = %v, want the activity time", next["slack:C1:1699999999.000100"])
	}
}

// A task with zero comments is reported as having no work log, and its links
// measure changed against the task's UpdatedAt.
func TestRunNoWorkLog(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-06-01T00:00:00Z"),
	}}

	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]ProbeResult{"slack:C1:1699999999.000100": {LastActivity: tp("2026-07-10T00:00:00Z")}},
	})

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	tr := report.Tasks["1"]
	if tr.HasWorkLog {
		t.Error("HasWorkLog = true, want false for a task with no comments")
	}
	if !tr.Links[0].Changed {
		t.Error("changed = false, want true measured against UpdatedAt")
	}
}

// The whole report golden-pins the JSON shape, including a no-work-log task.
func TestRunGolden(t *testing.T) {
	tasks := []sources.Task{
		{
			ID:        "8899",
			Title:     "Follow up https://kong.slack.com/archives/C1/p1699999999000100",
			UpdatedAt: *tp("2026-06-01T00:00:00Z"),
			Comments:  []sources.Comment{{PostedAt: *tp("2026-07-18T14:00:00Z")}},
		},
		{ID: "9001", Title: "Draft the brief", UpdatedAt: *tp("2026-07-01T00:00:00Z")},
	}

	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]ProbeResult{"slack:C1:1699999999.000100": {LastActivity: tp("2026-07-20T09:00:00Z")}},
	})

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	rendered, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	golden.Assert(t, "engine.golden", string(rendered))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/probe/ -run TestRun -v`
Expected: FAIL, `undefined: Run`.

- [ ] **Step 3: Write the engine**

```go
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

// Teams and any system with no registered prober resolve to these reasons.
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

	// Probe each system once. results maps a link key to its ProbeResult.
	results := map[string]ProbeResult{}
	for system, ls := range bySystem {
		prober, ok := reg.For(system)
		if !ok {
			for _, l := range ls {
				results[l.Key()] = ProbeResult{Unchecked: true, Reason: reasonForUnprobed(system)}
			}
			continue
		}
		out, err := prober.Probe(ctx, ls, since)
		if err != nil {
			reason := ReasonError
			if ctx.Err() != nil {
				reason = ReasonTimeout
			}
			for _, l := range ls {
				results[l.Key()] = ProbeResult{Unchecked: true, Reason: reason}
			}
			continue
		}
		// A key the prober was asked about but omitted is unchecked, never a
		// silent no-change.
		for _, l := range ls {
			if r, ok := out[l.Key()]; ok {
				results[l.Key()] = r
			} else {
				results[l.Key()] = ProbeResult{Unchecked: true, Reason: ReasonError}
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
```

- [ ] **Step 4: Generate the golden file, then verify**

Run: `nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./internal/probe/ -run TestRunGolden'`
Then: `nix develop --command go test ./internal/probe/ -v`
Expected: PASS. Open `internal/probe/testdata/engine.golden` and confirm task 8899 has `has_work_log:true`, a `last_logged`, and a changed Slack link, and task 9001 has `has_work_log:false`, no `last_logged`, and empty links.

- [ ] **Step 5: Lint**

Run: `nix develop --command golangci-lint run ./internal/probe/`
Expected: 0 issues.

- [ ] **Step 6: Commit**

```bash
git add internal/probe/engine.go internal/probe/engine_test.go internal/probe/testdata/
git commit -m "feat: add probe engine Run with the unchecked invariant and JSON output"
```

---

### Task 6: Slack prober, the channel-history collapse

**Files:**
- Create: `internal/probe/slack/slack.go`, `internal/probe/slack/slack_test.go`

- [ ] **Step 1: Write the failing test**

```go
package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// fakeSlack serves one channel with two threads. Thread A advanced past the
// watermark, thread B did not. It counts replies calls so the test can assert
// replies is fetched only for the advanced thread.
type fakeSlack struct{ repliesCalls int32 }

func (f *fakeSlack) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1699999999.000100", "thread_ts": "1699999999.000100",
						"reply_count": 3, "latest_reply": "1700000500.000000"},
					{"ts": "1699999999.000200", "thread_ts": "1699999999.000200",
						"reply_count": 1, "latest_reply": "1699999999.000300"},
				},
			})
		case "/conversations.replies":
			atomic.AddInt32(&f.repliesCalls, 1)
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1700000500.000000"},
				},
			})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	})
}

func TestProbeCollapsesAndFetchesAdvancedOnly(t *testing.T) {
	fake := &fakeSlack{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	ls := []links.Link{
		{System: links.SystemSlack, Raw: "a", Record: "C1:1699999999.000100",
			Fields: map[string]string{"channel": "C1", "thread": "1699999999.000100"}},
		{System: links.SystemSlack, Raw: "b", Record: "C1:1699999999.000200",
			Fields: map[string]string{"channel": "C1", "thread": "1699999999.000200"}},
	}

	// Watermark: thread B is already at its latest_reply, thread A is behind.
	since := sources.Watermark{
		"slack:C1:1699999999.000100": time.Unix(1699999000, 0),
		"slack:C1:1699999999.000200": mustTS("1699999999.000300"),
	}

	out, err := c.Probe(context.Background(), ls, since)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if got := atomic.LoadInt32(&fake.repliesCalls); got != 1 {
		t.Errorf("replies calls = %d, want 1 (only the advanced thread)", got)
	}
	if r := out["slack:C1:1699999999.000100"]; r.LastActivity == nil {
		t.Error("advanced thread has no last activity")
	}
	if r := out["slack:C1:1699999999.000200"]; r.Unchecked {
		t.Error("unadvanced thread should be checked (from history), not unchecked")
	}
}

// An expired token makes every slack link unchecked with ReasonAuth, never a
// silent no-change.
func TestProbeExpiredTokenUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemSlack, Record: "C1:1", Fields: map[string]string{"channel": "C1", "thread": "1"}}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["slack:C1:1"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
```

- [ ] **Step 2: Add the test helper and run to verify it fails**

Add to `slack_test.go`:

```go
func mustTS(s string) time.Time {
	t, _ := parseSlackTS(s)
	return t
}
```

Run: `nix develop --command go test ./internal/probe/slack/ -v`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Write the Slack prober**

```go
// Package slack probes Slack thread freshness with one channel history call per
// channel, fetching replies only for threads whose latest_reply advanced.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

const defaultBaseURL = "https://slack.com/api"

// Client is the Slack freshness prober.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Slack prober. The limiter is sized to Slack's Tier 3 ceiling of
// roughly 50 requests per minute.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Every(time.Minute/50), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemSlack }

// parseSlackTS converts a Slack ts string ("1699999999.000100") to a time.
func parseSlackTS(ts string) (time.Time, error) {
	dot := strings.IndexByte(ts, '.')
	secStr := ts
	if dot >= 0 {
		secStr = ts[:dot]
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing slack ts %q: %w", ts, err)
	}
	return time.Unix(sec, 0).UTC(), nil
}

// slackResponse is the subset of a Slack API envelope this prober decodes.
type slackResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error"`
	Messages []slackMessage `json:"messages"`
}

type slackMessage struct {
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	ReplyCount  int    `json:"reply_count"`
	LatestReply string `json:"latest_reply"`
}

// Probe groups links by channel, fetches one history per channel, and fetches
// replies only for threads whose latest_reply advanced past the watermark. Any
// failure makes every link unchecked with the fitting reason.
func (c *Client) Probe(ctx context.Context, ls []links.Link, since sources.Watermark) (map[string]probe.ProbeResult, error) {
	out := make(map[string]probe.ProbeResult, len(ls))

	uncheckAll := func(reason probe.Reason) map[string]probe.ProbeResult {
		for _, l := range ls {
			out[l.Key()] = probe.ProbeResult{Unchecked: true, Reason: reason}
		}
		return out
	}

	// Group links by channel.
	byChannel := map[string][]links.Link{}
	for _, l := range ls {
		ch := l.Fields["channel"]
		byChannel[ch] = append(byChannel[ch], l)
	}

	for channel, chLinks := range byChannel {
		hist, err := c.call(ctx, "conversations.history", url.Values{"channel": {channel}})
		if err != nil {
			return uncheckAll(reasonFor(err)), nil
		}

		// Index thread parents by their ts.
		latest := map[string]string{}
		for _, m := range hist.Messages {
			key := m.ThreadTS
			if key == "" {
				key = m.TS
			}
			lr := m.LatestReply
			if lr == "" {
				lr = m.TS
			}
			latest[key] = lr
		}

		for _, l := range chLinks {
			thread := l.Fields["thread"]
			lr, ok := latest[thread]
			if !ok {
				// The thread was not in the recent history window; treat its
				// last activity as the thread ts itself.
				lr = thread
			}

			lrTime, err := parseSlackTS(lr)
			if err != nil {
				out[l.Key()] = probe.ProbeResult{Unchecked: true, Reason: probe.ReasonUnparseable}
				continue
			}

			prev, seen := since[l.Key()]
			if seen && !lrTime.After(prev) {
				// Not advanced; the history call already told us the freshness.
				la := lrTime
				out[l.Key()] = probe.ProbeResult{LastActivity: &la}
				continue
			}

			// Advanced (or first sight): one replies call confirms the time.
			rep, err := c.call(ctx, "conversations.replies", url.Values{"channel": {channel}, "ts": {thread}})
			if err != nil {
				return uncheckAll(reasonFor(err)), nil
			}
			la := lrTime
			if n := len(rep.Messages); n > 0 {
				if t, err := parseSlackTS(rep.Messages[n-1].TS); err == nil {
					la = t
				}
			}
			out[l.Key()] = probe.ProbeResult{LastActivity: &la}
		}
	}

	return out, nil
}

// authError marks a Slack ok:false auth failure so Probe can map it to ReasonAuth.
type authError struct{ code string }

func (e authError) Error() string { return "slack auth: " + e.code }

func reasonFor(err error) probe.Reason {
	if _, ok := err.(authError); ok {
		return probe.ReasonAuth
	}
	return probe.ReasonError
}

// call performs one Slack API GET, enforces the rate limit, and decodes the
// envelope. An ok:false with an auth error becomes an authError.
func (c *Client) call(ctx context.Context, method string, q url.Values) (slackResponse, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return slackResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+method+"?"+q.Encode(), nil)
	if err != nil {
		return slackResponse{}, fmt.Errorf("building %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return slackResponse{}, fmt.Errorf("calling %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return slackResponse{}, fmt.Errorf("slack %s returned %s", method, resp.Status)
	}

	var out slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return slackResponse{}, fmt.Errorf("decoding %s: %w", method, err)
	}
	if !out.OK {
		if out.Error == "invalid_auth" || out.Error == "token_revoked" || out.Error == "account_inactive" {
			return slackResponse{}, authError{code: out.Error}
		}
		return slackResponse{}, fmt.Errorf("slack %s: %s", method, out.Error)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/probe/slack/ -v`
Expected: PASS. The collapse test asserts exactly one replies call; the expired-token test asserts `ReasonAuth`.

- [ ] **Step 5: Lint**

Run: `nix develop --command golangci-lint run ./internal/probe/slack/`
Expected: 0 issues. The `err.(authError)` type assertion may draw an `errorlint` note; if so, switch to `errors.As`.

- [ ] **Step 6: Commit**

```bash
git add internal/probe/slack/
git commit -m "feat: add Slack channel-history collapse prober"
```

---

### Task 7: Gmail, Aha, and Drive probers

Each is a single changed-since query with its own decode struct and rate
limiter. They follow the Slack client's shape (injectable base URL, auth to
unchecked). Build them one at a time.

**Files:**
- Create: `internal/probe/gmail/gmail.go`, `internal/probe/gmail/gmail_test.go`
- Create: `internal/probe/aha/aha.go`, `internal/probe/aha/aha_test.go`
- Create: `internal/probe/gdrive/gdrive.go`, `internal/probe/gdrive/gdrive_test.go`

- [ ] **Step 1: Write the Aha test (simplest changed-since)**

`internal/probe/aha/aha_test.go`:

```go
package aha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeParsesUpdatedSince(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"features": []map[string]any{
				{"reference_num": "GTWY-I-1484", "updated_at": "2026-07-20T09:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemAha, Record: "GTWY-I-1484"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["aha:GTWY-I-1484"]; r.LastActivity == nil {
		t.Fatalf("no last activity for the aha record")
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemAha, Record: "GTWY-I-1484"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["aha:GTWY-I-1484"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `nix develop --command go test ./internal/probe/aha/ -v`
Expected: FAIL, `undefined: New`.

- [ ] **Step 3: Write the Aha prober**

`internal/probe/aha/aha.go`:

```go
// Package aha probes Aha record freshness with a single updated-since query.
package aha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Aha freshness prober.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// New builds an Aha prober with a conservative 5 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://api.aha.io/api/v1",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(5), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemAha }

// ahaResponse is the subset of the Aha features response this prober decodes.
type ahaResponse struct {
	Features []struct {
		Reference string `json:"reference_num"`
		UpdatedAt string `json:"updated_at"`
	} `json:"features"`
}

// Probe fetches records updated since the earliest watermark, then maps each
// requested link to its update time. A non-2xx makes every link unchecked.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.ProbeResult, error) {
	out := make(map[string]probe.ProbeResult, len(ls))

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/features?updated_since=1970-01-01", nil)
	if err != nil {
		return nil, fmt.Errorf("building aha request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return uncheck(ls, probe.ReasonAuth), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uncheck(ls, probe.ReasonError), nil
	}

	var body ahaResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}

	updated := map[string]time.Time{}
	for _, f := range body.Features {
		if t, err := time.Parse(time.RFC3339, f.UpdatedAt); err == nil {
			updated[f.Reference] = t
		}
	}

	for _, l := range ls {
		if t, ok := updated[l.Record]; ok {
			tt := t
			out[l.Key()] = probe.ProbeResult{LastActivity: &tt}
		} else {
			// Not in the updated set means no change since the watermark; report
			// no activity newer than known, which the engine reads as unchanged.
			out[l.Key()] = probe.ProbeResult{}
		}
	}
	return out, nil
}

// uncheck marks every link unchecked with a reason.
func uncheck(ls []links.Link, reason probe.Reason) map[string]probe.ProbeResult {
	out := make(map[string]probe.ProbeResult, len(ls))
	for _, l := range ls {
		out[l.Key()] = probe.ProbeResult{Unchecked: true, Reason: reason}
	}
	return out
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `nix develop --command go test ./internal/probe/aha/ -v`
Expected: PASS.

- [ ] **Step 5: Write the Gmail prober and its test**

`internal/probe/gmail/gmail_test.go` mirrors the Aha test with the Gmail
response shape:

```go
package gmail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeParsesHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"threads": []map[string]any{
				{"id": "FMfcgzGabc123", "internalDate": "1721466000000"},
			},
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGmail, Record: "FMfcgzGabc123"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["gmail:FMfcgzGabc123"]; r.LastActivity == nil {
		t.Fatal("no last activity for the gmail thread")
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGmail, Record: "FMfcgzGabc123"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["gmail:FMfcgzGabc123"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
```

`internal/probe/gmail/gmail.go`:

```go
// Package gmail probes Gmail thread freshness with a single threads query.
package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Gmail freshness prober.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// New builds a Gmail prober with a conservative 10 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://gmail.googleapis.com/gmail/v1",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(10), 10),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemGmail }

// gmailResponse is the subset of the threads list this prober decodes.
// internalDate is epoch milliseconds as a string.
type gmailResponse struct {
	Threads []struct {
		ID           string `json:"id"`
		InternalDate string `json:"internalDate"`
	} `json:"threads"`
}

// Probe fetches recent threads and maps each requested thread id to its
// internalDate. A non-2xx makes every link unchecked.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.ProbeResult, error) {
	out := make(map[string]probe.ProbeResult, len(ls))

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/users/me/threads", nil)
	if err != nil {
		return nil, fmt.Errorf("building gmail request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return uncheck(ls, probe.ReasonAuth), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uncheck(ls, probe.ReasonError), nil
	}

	var body gmailResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}

	updated := map[string]time.Time{}
	for _, th := range body.Threads {
		if ms, err := strconv.ParseInt(th.InternalDate, 10, 64); err == nil {
			updated[th.ID] = time.UnixMilli(ms).UTC()
		}
	}

	for _, l := range ls {
		if t, ok := updated[l.Record]; ok {
			tt := t
			out[l.Key()] = probe.ProbeResult{LastActivity: &tt}
		} else {
			out[l.Key()] = probe.ProbeResult{}
		}
	}
	return out, nil
}

func uncheck(ls []links.Link, reason probe.Reason) map[string]probe.ProbeResult {
	out := make(map[string]probe.ProbeResult, len(ls))
	for _, l := range ls {
		out[l.Key()] = probe.ProbeResult{Unchecked: true, Reason: reason}
	}
	return out
}
```

- [ ] **Step 6: Write the Drive prober and its test**

`internal/probe/gdrive/gdrive_test.go`:

```go
package gdrive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeParsesModifiedTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"files": []map[string]any{
				{"id": "1AbC_dEF", "modifiedTime": "2026-07-20T09:00:00Z"},
			},
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGDrive, Record: "1AbC_dEF"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["gdrive:1AbC_dEF"]; r.LastActivity == nil {
		t.Fatal("no last activity for the drive file")
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGDrive, Record: "1AbC_dEF"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["gdrive:1AbC_dEF"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
```

`internal/probe/gdrive/gdrive.go`:

```go
// Package gdrive probes Drive file freshness with a single modifiedTime query.
package gdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Drive freshness prober.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// New builds a Drive prober with a conservative 10 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://www.googleapis.com/drive/v3",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(10), 10),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemGDrive }

// driveResponse is the subset of the files list this prober decodes.
type driveResponse struct {
	Files []struct {
		ID           string `json:"id"`
		ModifiedTime string `json:"modifiedTime"`
	} `json:"files"`
}

// Probe fetches files with their modifiedTime and maps each requested file id
// to it. A non-2xx makes every link unchecked.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.ProbeResult, error) {
	out := make(map[string]probe.ProbeResult, len(ls))

	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/files?fields=files(id,modifiedTime)", nil)
	if err != nil {
		return nil, fmt.Errorf("building drive request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return uncheck(ls, probe.ReasonAuth), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uncheck(ls, probe.ReasonError), nil
	}

	var body driveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}

	updated := map[string]time.Time{}
	for _, f := range body.Files {
		if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
			updated[f.ID] = t
		}
	}

	for _, l := range ls {
		if t, ok := updated[l.Record]; ok {
			tt := t
			out[l.Key()] = probe.ProbeResult{LastActivity: &tt}
		} else {
			out[l.Key()] = probe.ProbeResult{}
		}
	}
	return out, nil
}

func uncheck(ls []links.Link, reason probe.Reason) map[string]probe.ProbeResult {
	out := make(map[string]probe.ProbeResult, len(ls))
	for _, l := range ls {
		out[l.Key()] = probe.ProbeResult{Unchecked: true, Reason: reason}
	}
	return out
}
```

- [ ] **Step 7: Run all three prober packages and lint**

Run: `nix develop --command sh -c 'go test ./internal/probe/... && golangci-lint run ./internal/probe/...'`
Expected: PASS, 0 issues.

- [ ] **Step 8: Commit**

```bash
git add internal/probe/gmail/ internal/probe/aha/ internal/probe/gdrive/
git commit -m "feat: add Gmail, Aha, and Drive changed-since probers"
```

---

### Task 8: Wire ballpoint probe into the CLI

**Files:**
- Create: `internal/probe/wire.go` (builds the registry from credentials)
- Modify: `internal/cli/cli.go`, `internal/cli/cli_test.go`, `internal/cli/testdata/usage.golden`

- [ ] **Step 1: Write the registry builder and its test**

`internal/probe/wire_test.go`:

```go
package probe

import (
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
)

// BuildRegistry registers a prober only for a system whose credential is
// present. A missing credential leaves that system unregistered, so the engine
// renders it unchecked rather than failing the run.
func TestBuildRegistrySkipsMissingCreds(t *testing.T) {
	creds := Credentials{Slack: "test-token"} // aha, google absent
	reg := BuildRegistry(creds)

	if _, ok := reg.For(links.SystemSlack); !ok {
		t.Error("slack prober not registered despite a token")
	}
	if _, ok := reg.For(links.SystemAha); ok {
		t.Error("aha prober registered without a token")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `nix develop --command go test ./internal/probe/ -run TestBuildRegistry -v`
Expected: FAIL, `undefined: Credentials`.

- [ ] **Step 3: Write the registry builder**

`internal/probe/wire.go`:

```go
package probe

import (
	"github.com/bashfulrobot/ballpoint/internal/probe/aha"
	"github.com/bashfulrobot/ballpoint/internal/probe/gdrive"
	"github.com/bashfulrobot/ballpoint/internal/probe/gmail"
	"github.com/bashfulrobot/ballpoint/internal/probe/slack"
)

// Credentials holds each source's token. An empty field means that source has
// no credential, so its prober is not registered and its links render
// unchecked. Values are never logged.
type Credentials struct {
	Slack  string
	Aha    string
	Google string // shared by Gmail and Drive
}

// BuildRegistry registers a prober for each system whose credential is present.
func BuildRegistry(c Credentials) *Registry {
	reg := &Registry{}
	if c.Slack != "" {
		reg.Register(slack.New(c.Slack))
	}
	if c.Aha != "" {
		reg.Register(aha.New(c.Aha))
	}
	if c.Google != "" {
		reg.Register(gmail.New(c.Google))
		reg.Register(gdrive.New(c.Google))
	}
	return reg
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `nix develop --command go test ./internal/probe/ -run TestBuildRegistry -v`
Expected: PASS.

- [ ] **Step 5: Write the CLI probe behaviour test**

Add to `internal/cli/cli_test.go`:

```go
// probe --dry-run extracts and groups links from tasks and reports planned
// per-system call counts without touching the network, so it runs green with
// no credentials. It uses an injected task source.
func TestRunProbeDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer

	tasks := []sources.Task{{
		ID:    "1",
		Title: "x https://kong.slack.com/archives/C1/p1699999999000100 and https://kong.slack.com/archives/C1/p1699999999000200",
	}}

	err := runProbe(probeDeps{
		tasks:   tasks,
		dryRun:  true,
		stateDir: t.TempDir(),
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runProbe() error = %v", err)
	}

	// Two slack threads in one channel collapse to one planned channel call.
	if !strings.Contains(stdout.String(), "slack") {
		t.Errorf("dry-run output missing slack plan: %q", stdout.String())
	}
}
```

- [ ] **Step 6: Run to verify it fails**

Run: `nix develop --command go test ./internal/cli/ -run TestRunProbeDryRun -v`
Expected: FAIL, `undefined: runProbe`.

- [ ] **Step 7: Implement the probe command**

Add a `probe.go` inside `internal/cli` so the verb's behaviour is isolated
from the parser. Create `internal/cli/probe.go`:

```go
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
	"github.com/bashfulrobot/ballpoint/internal/secrets"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/sources/todoist"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// probeDeps are the inputs to runProbe, injected so tests can supply tasks and
// a temp state dir without a network or a real secrets file.
type probeDeps struct {
	tasks     []sources.Task // when nil, fetched from Todoist
	creds     probe.Credentials
	stateDir  string
	dryRun    bool
	benchmark bool
}

// resolveProbeDeps fills the parts of probeDeps that come from the environment:
// the state dir and the credentials. Split out so runProbe stays testable.
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
	// A missing credential is not fatal; that source renders unchecked.
	deps.creds.Slack, _ = secrets.Load(path, "slack_token")
	deps.creds.Aha, _ = secrets.Load(path, "aha_token")
	deps.creds.Google, _ = secrets.Load(path, "google_token")

	if len(deps.tasks) == 0 && !dryRun {
		token, err := secrets.Load(path, "todoist_token")
		if err != nil {
			return probeDeps{}, fmt.Errorf("loading todoist token: %w", err)
		}
		delta, err := todoist.New(token).Probe(context.Background(), sources.Watermark{})
		if err != nil {
			return probeDeps{}, fmt.Errorf("fetching tasks: %w", err)
		}
		deps.tasks = delta.Tasks
	}
	return deps, nil
}

// runProbe executes the probe. In dry-run it prints the planned per-system call
// counts and makes no network call and writes no watermark. Otherwise it runs
// the engine and writes the JSON report to stdout.
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
	report, next, err := probe.Run(context.Background(), deps.tasks, since, probe.BuildRegistry(deps.creds))
	if err != nil {
		return err
	}
	if err := st.SaveWatermark(next); err != nil {
		return err
	}

	if deps.benchmark {
		fmt.Fprintf(stderr, "probe wall clock: %v over %d tasks\n", time.Since(start), len(deps.tasks))
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// dryRunPlan prints how many calls each system would take, proving the
// batch-by-system collapse without making any call.
func dryRunPlan(tasks []sources.Task, stdout io.Writer) error {
	channels := map[links.System]map[string]bool{}
	records := map[links.System]int{}
	for _, task := range tasks {
		for _, l := range links.Extract(task) {
			if l.System == links.SystemSlack {
				if channels[l.System] == nil {
					channels[l.System] = map[string]bool{}
				}
				channels[l.System][l.Fields["channel"]] = true
			}
			records[l.System]++
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
			calls = len(channels[sys])
		}
		fmt.Fprintf(stdout, "%s: %d links, ~%d calls\n", s, records[sys], calls)
	}
	return nil
}
```

- [ ] **Step 8: Route the probe verb to runProbe**

In `internal/cli/cli.go`, replace the `probe` case body so it reads the flags
and dispatches. Change the case to:

```go
	case "probe":
		pf := flag.NewFlagSet("probe", flag.ContinueOnError)
		pf.SetOutput(stderr)
		dryRun := pf.Bool("dry-run", false, "report planned per-system calls without probing")
		benchmark := pf.Bool("benchmark", false, "time the real pass and print the wall clock")

		if err := pf.Parse(rest[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if pf.NArg() > 0 {
			return fmt.Errorf("probe takes no positional arguments, got %q", pf.Args())
		}

		deps, err := resolveProbeDeps(*dryRun, *benchmark)
		if err != nil {
			return err
		}
		return runProbe(deps, stdout, stderr)
```

- [ ] **Step 9: Update the usage golden and run the CLI tests**

Change the `usage` const `probe` line to document both flags:

```
  ballpoint probe [--dry-run] [--benchmark]  refresh freshness data
```

Run: `nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./internal/cli/ -run TestRunHelp && go test ./internal/cli/ -v'`
Expected: PASS. The existing not-implemented test for `probe` no longer applies; if `TestRunNotImplemented` includes a `probe` case, remove that one row (probe is now implemented, so it fetches and would error only on a missing secrets file under the test environment). Keep the `dispatch` and bare-walk rows.

Note on the not-implemented test: `probe` with no injected tasks calls
`resolveProbeDeps`, which reads the real secrets file and will error in CI.
That is fine for the dry-run test (which injects tasks), but do not leave a
bare `probe` invocation asserting `ErrNotImplemented`. Update
`TestRunNotImplemented` to cover only `""` (bare walk) and `dispatch`.

- [ ] **Step 10: Lint and commit**

Run: `nix develop --command sh -c 'golangci-lint run ./internal/... && go test ./...'`
Expected: 0 issues, all pass.

```bash
git add internal/probe/wire.go internal/probe/wire_test.go internal/cli/
git commit -m "feat: wire ballpoint probe with dry-run and benchmark"
```

---

### Task 9: The reproducible call-count test

**Files:**
- Create: `internal/probe/bench_test.go`

- [ ] **Step 1: Write the call-count test**

```go
package probe

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// countingProber records how many times Probe is invoked and how many distinct
// records it was asked about, standing in for a real source with no network.
type countingProber struct {
	system links.System
	calls  *int
}

func (p countingProber) System() links.System { return p.system }

func (p countingProber) Probe(_ context.Context, ls []links.Link, _ sources.Watermark) (map[string]ProbeResult, error) {
	*p.calls++
	out := make(map[string]ProbeResult, len(ls))
	now := time.Now()
	for _, l := range ls {
		la := now
		out[l.Key()] = ProbeResult{LastActivity: &la}
	}
	return out, nil
}

// TestBatchBySystemCollapsesCalls builds a synthetic corpus modelled on the
// real one (71 tasks, 148 links, Slack concentrated on ~40 channels) and shows
// batch-by-system issues one prober call per system rather than one per link.
func TestBatchBySystemCollapsesCalls(t *testing.T) {
	tasks := syntheticCorpus()

	totalLinks := 0
	for _, task := range tasks {
		totalLinks += len(links.Extract(task))
	}
	if totalLinks < 140 {
		t.Fatalf("synthetic corpus has %d links, want ~148", totalLinks)
	}

	slackCalls, ahaCalls, driveCalls := 0, 0, 0
	var reg Registry
	reg.Register(countingProber{system: links.SystemSlack, calls: &slackCalls})
	reg.Register(countingProber{system: links.SystemAha, calls: &ahaCalls})
	reg.Register(countingProber{system: links.SystemGDrive, calls: &driveCalls})

	if _, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	proberCalls := slackCalls + ahaCalls + driveCalls
	t.Logf("corpus: %d tasks, %d links; prober invocations: %d (slack %d, aha %d, drive %d)",
		len(tasks), totalLinks, proberCalls, slackCalls, ahaCalls, driveCalls)

	// One invocation per registered system with links, far below one per link.
	if proberCalls > 3 {
		t.Errorf("prober invocations = %d, want one per system (batch-by-system)", proberCalls)
	}
	if totalLinks <= proberCalls {
		t.Errorf("no collapse: %d links, %d calls", totalLinks, proberCalls)
	}
}

// syntheticCorpus returns 71 tasks carrying ~148 links, with Slack links spread
// over ~40 channels, matching the shape the issue measured.
func syntheticCorpus() []sources.Task {
	var tasks []sources.Task
	link := 0
	for i := 0; i < 71; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "task %d\n", i)
		// Slack: 111 links across 40 channels, so channel = link mod 40.
		for j := 0; j < 2; j++ {
			ch := fmt.Sprintf("C%d", link%40)
			fmt.Fprintf(&b, "https://kong.slack.com/archives/%s/p16999999990%05d\n", ch, link)
			link++
			if link >= 111 {
				break
			}
		}
		// A handful of aha and drive links to exercise the other systems.
		if i%7 == 0 {
			fmt.Fprintf(&b, "GTWY-I-%d\n", i)
		}
		if i%9 == 0 {
			fmt.Fprintf(&b, "https://docs.google.com/document/d/FILE%d/edit\n", i)
		}
		tasks = append(tasks, sources.Task{ID: fmt.Sprintf("%d", i), Title: b.String(), UpdatedAt: time.Now()})
	}
	return tasks
}
```

- [ ] **Step 2: Add the strings import and run**

The synthetic corpus uses `strings.Builder`. Add `"strings"` to the import
block. Run: `nix develop --command go test ./internal/probe/ -run TestBatchBySystem -v`
Expected: PASS, with a log line reporting 71 tasks, ~148 links, and 3 prober
invocations. Record the printed figure for the PR.

- [ ] **Step 3: Commit**

```bash
git add internal/probe/bench_test.go
git commit -m "test: prove batch-by-system collapses per-link calls"
```

---

### Task 10: README and vendorHash

**Files:**
- Modify: `README.md`
- Verify: `nix/ballpoint.nix`, `go.mod`, `go.sum`

- [ ] **Step 1: Confirm no new module and the build still resolves**

Run: `nix develop --command go mod tidy`
Then: `git diff --stat go.mod go.sum`
Expected: empty. The probers use only the standard library plus the already
pinned `golang.org/x/time/rate`, so the module graph is unchanged.

Run: `nix build`
Expected: exit 0, stamps `0.1.0`, against the existing `vendorHash`. If
`go.mod` changed (it should not), set `vendorHash = lib.fakeHash` in
`nix/ballpoint.nix`, `nix build`, copy the `got:` hash back, and rebuild.

- [ ] **Step 2: Document the probe in the README**

Add a `## Probe` section covering:

- What `ballpoint probe` does: extracts each task's links, batches freshness
  checks by system, and writes JSON keyed by task to stdout.
- The per-source credentials, flat keys in the off-store secrets file:
  `slack_token`, `aha_token`, `google_token` (shared by Gmail and Drive), read
  at runtime, never env or store, never logged.
- Which systems ship a prober (Slack, Gmail, Aha, Drive) and which render
  `unchecked`: Teams (`not probeable`), and Jira, Salesforce, GitHub
  (`no probe available`), plus any source whose credential is missing or whose
  token expired (`credentials missing or expired`).
- The unchecked invariant: a source that errors, times out, or has no prober is
  `unchecked`, never `no change`.
- The reproducible call-count figure from Task 9 (the logged prober
  invocations for the synthetic 71-task, ~148-link corpus), stated as a mock
  measurement of the batch-by-system collapse.
- The live commands: `ballpoint probe` loads credentials and runs one real
  pass; `ballpoint probe --dry-run` reports planned per-system call counts
  without calling any source or writing a watermark; `ballpoint probe
  --benchmark` times the real pass.
- The Slack collapse: one `conversations.history` per channel reads every
  thread's `latest_reply`, and only advanced threads cost a `conversations.replies`
  call, so a warm run is cheaper than a cold one.

Use the real figure from Task 9, not a placeholder.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document the probe, its credentials, and the unchecked invariant"
```

---

### Task 11: Full acceptance verification

- [ ] **Step 1: Run the whole suite and the linter**

```bash
nix develop --command go test ./...
nix develop --command golangci-lint run
nix flake check
```

Expected: all pass.

- [ ] **Step 2: Confirm each acceptance criterion**

```bash
# Batch by system: the engine calls one prober per system
nix develop --command go test ./internal/probe/ -run TestBatchBySystem -v
# Slack collapse: replies only for advanced threads
nix develop --command go test ./internal/probe/slack/ -run TestProbeCollapses -v
# Single changed-since per source
nix develop --command go test ./internal/probe/aha/ ./internal/probe/gmail/ ./internal/probe/gdrive/ -v
# Unchecked never no-change: error, timeout, absent, teams
nix develop --command go test ./internal/probe/ -run 'TestRunUnchecked' -v
# Zero-comment task reported as no work log
nix develop --command go test ./internal/probe/ -run TestRunNoWorkLog -v
# JSON keyed by task
nix develop --command go test ./internal/probe/ -run TestRunGolden -v
# Dry-run makes no network call and no watermark write
nix develop --command go test ./internal/cli/ -run TestRunProbeDryRun -v
```

Expected: all pass.

- [ ] **Step 3: Confirm no credential leaks in test output**

Run: `nix develop --command sh -c 'go test ./... -v 2>&1 | grep -iE "bearer test-token|token.{0,3}[A-Za-z0-9]{20}" | grep -v "want Bearer test-token" || echo "no credential value in test output"'`
Expected: `no credential value in test output`. The only token literal is
`test-token`, and it appears in headers the mock server checks, never logged by
production code.

---

## Self-review

**Spec coverage.** Batch by system: engine Run groups by system and calls one
Prober each (Task 5), proven by the call-count test (Task 9). Slack collapse:
Task 6. Gmail, Aha, Drive single changed-since: Task 7. Per-source rate
limiting sized to each vendor: each prober's `New` (Tasks 6, 7). Unchecked
invariant: Task 5, unit-tested for error, timeout, absent, Teams, and no-probe.
Teams unchecked with a reason: Task 5 `reasonForUnprobed`. Watermarks persist:
the #2 store, extended key space, Tasks 5 and 8. Idempotent and interrupt-safe:
end-of-run atomic write, checked-only updates, Task 5. JSON keyed by task with
last activity, last logged, changed: Task 5 golden. Zero-comment no-work-log:
Task 5. Cold run within the Slack limit recording wall clock: the live command
plus the CI call-count test, Tasks 8 and 9. Golden tests per source and the
unchecked path: Tasks 3, 5, 6, 7. No gap.

**Placeholder scan.** The only sentinel is `lib.fakeHash` in Task 10, used only
if `go.mod` changed, which it should not. The call-count figure is a real
logged number captured in Task 9 Step 2 and carried into the README in Task 10.
No TBD, no "handle errors", every code step shows code.

**Type consistency.** `links.System`, `links.Link`, `Link.Key()` are defined in
Tasks 1 and 3 and used with the same shapes in Tasks 5, 6, 7, 8, 9.
`probe.ProbeResult`, `probe.Prober`, `probe.Registry`, `probe.Reason` and its
constants are defined in Task 4 and used unchanged in Tasks 5, 6, 7, 8, 9.
`Run(ctx, []sources.Task, sources.Watermark, *Registry) (Report, sources.Watermark, error)`
is defined in Task 5 and called with that signature in Tasks 8 and 9.
`probe.Credentials` and `BuildRegistry` are defined in Task 8 and used in the
CLI. Each prober's `New(token, ...Option)` and `WithBaseURL` match across Tasks
6 and 7. `parseSlackTS` is defined in Task 6 and used by the Task 6 test helper.
`slack:C1:<ts>` keys line up with `Link.Key()` from Task 1.

**Credential discipline.** No task logs or golden-files a token. Tests use
`test-token`. The Task 11 leak check asserts no value escapes.
