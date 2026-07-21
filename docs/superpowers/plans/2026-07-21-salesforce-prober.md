# Salesforce Freshness Prober Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `internal/probe/salesforce`, a `probe.Prober` that answers Salesforce link freshness by shelling out to the `sf` CLI, and wire it in so Salesforce links report a last activity time and a `changed` flag instead of `unchecked`.

**Architecture:** Salesforce access reuses the already-authenticated `sf` CLI through an injectable command runner (a `func` seam), so tests drive it with recorded JSON envelopes and never touch a live org. `internal/links` grows Salesforce URL parsing (Lightning and classic) and a broadened bare-id regex; the prober groups the extracted records by sObject, issues one `sf data query ... --json` per group, and maps `LastModifiedDate` back per link. Every record and object name is charset-validated before it enters a SOQL string, and every failure path (missing CLI, non-zero `sf` status, query error, unmapped id prefix) renders the affected links `unchecked` and never advances a watermark.

**Tech Stack:** Go 1.26, `golang.org/x/time/rate`, `os/exec`, the existing `internal/probe` engine and `internal/links` extractor.

---

## File Structure

- `internal/links/parse.go` — add `parseSalesforce` (Lightning + classic record URLs). Modify.
- `internal/links/extract.go` — recognise `force.com`/`salesforce.com` hosts; broaden the `sfidRe` bare-id regex to the eight supported key prefixes. Modify.
- `internal/links/parse_test.go` — `parseSalesforce` unit tests. Modify.
- `internal/links/extract_test.go` — host recognition, broadened bare id, no-double-count-inside-URL. Modify.
- `internal/probe/salesforce/salesforce.go` — the prober: `Runner` seam, `Client`, `New`, `System`, `Available`, SOQL build, envelope decode, result mapping. Create.
- `internal/probe/salesforce/salesforce_test.go` — the prober's tests. Create.
- `internal/probe/probeset/probeset.go` — register the prober when Salesforce is available. Modify.
- `internal/probe/probeset/probeset_test.go` — registration tests. Modify.
- `internal/cli/probe.go` — set `creds.Salesforce` from `salesforce.Available()` in `resolveProbeDeps`. Modify.
- `README.md` — move Salesforce out of `no probe available`; document the CLI-auth model. Modify.

---

## Task 1: Salesforce URL parsing and bare-id recognition in `internal/links`

**Files:**
- Modify: `internal/links/parse.go`
- Modify: `internal/links/extract.go`
- Test: `internal/links/parse_test.go`, `internal/links/extract_test.go`

- [ ] **Step 1: Write the failing parse tests**

Add to `internal/links/parse_test.go`:

```go
func TestParseSalesforceLightning(t *testing.T) {
	rec, f := parseSalesforce("https://myorg.lightning.force.com/lightning/r/Opportunity/006XX000004Ci1wYAC/view")
	if rec != "006XX000004Ci1wYAC" {
		t.Errorf("record = %q, want the 18-char id", rec)
	}
	if f["object"] != "Opportunity" {
		t.Errorf("object = %q, want Opportunity", f["object"])
	}
}

func TestParseSalesforceClassic(t *testing.T) {
	rec, f := parseSalesforce("https://na1.salesforce.com/006XX000004Ci1w")
	if rec != "006XX000004Ci1w" {
		t.Errorf("record = %q, want the 15-char id", rec)
	}
	if _, ok := f["object"]; ok {
		t.Errorf("classic url carries no object hint, got %v", f)
	}
}

func TestParseSalesforceUnparseable(t *testing.T) {
	if rec, _ := parseSalesforce("https://myorg.lightning.force.com/lightning/o/Account/list"); rec != "" {
		t.Errorf("record = %q, want empty for a non-record salesforce url", rec)
	}
}
```

- [ ] **Step 2: Run the parse tests to verify they fail**

Run: `nix develop --command go test ./internal/links/ -run TestParseSalesforce`
Expected: FAIL — `undefined: parseSalesforce`.

- [ ] **Step 3: Implement `parseSalesforce`**

Add to `internal/links/parse.go` inside the existing `var ( ... )` regex block:

```go
	sfLightning = regexp.MustCompile(`/lightning/r/([A-Za-z][A-Za-z0-9_]*)/([0-9A-Za-z]{15,18})`)
	sfRecordID  = regexp.MustCompile(`/([0-9A-Za-z]{15,18})(?:[/?#]|$)`)
```

Add the function (place near `parseAha`):

```go
// parseSalesforce returns the record id from a Salesforce permalink. A Lightning
// record URL (/lightning/r/<Object>/<Id>/) also yields the object name, which the
// prober prefers over the id key prefix. A classic record URL (/<Id>) yields the
// id alone. A URL that is not a record permalink returns an empty record.
func parseSalesforce(raw string) (string, map[string]string) {
	if m := sfLightning.FindStringSubmatch(raw); m != nil {
		return m[2], map[string]string{"object": m[1], "record": m[2]}
	}
	if m := sfRecordID.FindStringSubmatch(raw); m != nil {
		return m[1], map[string]string{"record": m[1]}
	}
	return "", nil
}
```

- [ ] **Step 4: Run the parse tests to verify they pass**

Run: `nix develop --command go test ./internal/links/ -run TestParseSalesforce`
Expected: PASS.

- [ ] **Step 5: Write the failing extract tests**

Add to `internal/links/extract_test.go`:

```go
// A Lightning record URL is categorised as Salesforce, carries the object hint,
// and is counted once, not a second time as a bare id inside the URL.
func TestExtractSalesforceLightningURL(t *testing.T) {
	u := "https://myorg.lightning.force.com/lightning/r/Account/001XX000003DHPhYAO/view"
	task := sources.Task{ID: "sf1", Title: "acct " + u}

	var sf []Link
	for _, l := range Extract(task) {
		if l.System == SystemSalesforce {
			sf = append(sf, l)
		}
	}
	if len(sf) != 1 {
		t.Fatalf("salesforce link count = %d, want 1 (no bare double-count inside the URL)", len(sf))
	}
	if sf[0].Record != "001XX000003DHPhYAO" || sf[0].Fields["object"] != "Account" {
		t.Errorf("link = %+v, want record 001XX000003DHPhYAO object Account", sf[0])
	}
}

// A bare Case-prefixed id (500...) is recognised as Salesforce; the old regex
// only matched 00-prefixed ids.
func TestExtractSalesforceBareCaseID(t *testing.T) {
	task := sources.Task{ID: "sf2", Title: "record 500XX000001AbcdEAG here"}
	found := false
	for _, l := range Extract(task) {
		if l.System == SystemSalesforce && l.Record == "500XX000001AbcdEAG" {
			found = true
		}
	}
	if !found {
		t.Error("bare 500-prefixed id was not extracted as salesforce")
	}
}
```

- [ ] **Step 6: Run the extract tests to verify they fail**

Run: `nix develop --command go test ./internal/links/ -run TestExtractSalesforce`
Expected: FAIL — the Lightning URL categorises as `SystemURL`, and the 500-prefixed id is not matched.

- [ ] **Step 7: Recognise Salesforce hosts and broaden the bare-id regex**

In `internal/links/extract.go`, replace the `sfidRe` line in the `var ( ... )` block:

```go
	sfidRe = regexp.MustCompile(`\b(?:001|003|005|006|00Q|500|701|800)[0-9A-Za-z]{12}(?:[0-9A-Za-z]{3})?\b`)
```

In `categoriseURL`, add a case before the `github.com` case:

```go
	case strings.Contains(host, "force.com"), strings.Contains(host, "salesforce.com"):
		rec, f := parseSalesforce(raw)
		return Link{System: SystemSalesforce, Raw: raw, Record: rec, Fields: f}
```

- [ ] **Step 8: Run the full links suite to verify it passes**

Run: `nix develop --command go test ./internal/links/`
Expected: PASS (including the untouched `extract.golden`, which carries no Salesforce records so it is unaffected).

- [ ] **Step 9: Commit**

```bash
git add internal/links/
git commit -m "feat(links): parse Salesforce record URLs and broaden bare-id recognition"
```

---

## Task 2: The Salesforce prober

**Files:**
- Create: `internal/probe/salesforce/salesforce.go`
- Test: `internal/probe/salesforce/salesforce_test.go`

- [ ] **Step 1: Write the prober tests**

Create `internal/probe/salesforce/salesforce_test.go`:

```go
package salesforce

import (
	"context"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// recordRunner returns a canned envelope and records the exact args it received.
func recordRunner(out string, gotArgs *[]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*gotArgs = append([]string{name}, args...)
		return []byte(out), nil
	}
}

const okCaseEnvelope = `{"status":0,"result":{"records":[
	{"attributes":{"type":"Case"},"CaseNumber":"00012345","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
]}}`

// A record that is all digits is queried as a Case by CaseNumber, and the runner
// is invoked as `sf data query --query <soql> --json`.
func TestProbeCaseByNumber(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(okCaseEnvelope, &args)))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "00012345"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["salesforce:00012345"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	want := []string{"sf", "data", "query", "--query", "", "--json"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want shape %v", args, want)
	}
	for i, a := range want {
		if i == 4 {
			continue // the SOQL string, checked below
		}
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
	soql := args[4]
	if !strings.Contains(soql, "FROM Case") || !strings.Contains(soql, "CaseNumber IN ('00012345')") {
		t.Errorf("soql = %q, want a Case-by-CaseNumber query", soql)
	}
}

const okOppEnvelope = `{"status":0,"result":{"records":[
	{"attributes":{"type":"Opportunity"},"Id":"006XX000004Ci1wYAC","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
]}}`

// The object comes from the Lightning URL hint, not the id prefix map.
func TestProbeObjectFromURL(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(okOppEnvelope, &args)))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "006XX000004Ci1wYAC",
		Fields: map[string]string{"object": "Opportunity", "record": "006XX000004Ci1wYAC"},
	}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["salesforce:006XX000004Ci1wYAC"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	if !strings.Contains(args[4], "FROM Opportunity") {
		t.Errorf("soql = %q, want FROM Opportunity", args[4])
	}
}

// An id whose 3-char prefix is not in the map, with no object hint, renders
// unchecked with the no-probe reason and never reaches the runner.
func TestProbeUnknownPrefixUnchecked(t *testing.T) {
	called := false
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	c := New(WithRunner(runner))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "999XX000001AbcdEAG"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	r := out["salesforce:999XX000001AbcdEAG"]
	if !r.Unchecked || r.Reason != probe.ReasonNoProbe {
		t.Errorf("result = %+v, want unchecked with ReasonNoProbe", r)
	}
	if called {
		t.Error("runner was called for an unmapped-prefix id; it must be filtered first")
	}
}

// A non-zero sf status whose message is an auth failure renders ReasonAuth; a
// generic failure renders ReasonError.
func TestProbeCLIFailureAuthVsError(t *testing.T) {
	authEnv := `{"status":1,"name":"NoOrgFound","message":"No authorization information found for org."}`
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(authEnv), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("auth result = %+v, want unchecked ReasonAuth", r)
	}

	errEnv := `{"status":1,"name":"InvalidQuery","message":"unexpected token near WHERE"}`
	c2 := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(errEnv), nil
	}))
	out2, _ := c2.Probe(context.Background(), ls, sources.Watermark{})
	if r := out2["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("error result = %+v, want unchecked ReasonError", r)
	}
}

// A runner that fails to produce any parseable envelope (e.g. the binary is
// missing) renders ReasonError, never a false unchanged.
func TestProbeRunnerErrorUnchecked(t *testing.T) {
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, context.Canceled
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked {
		t.Errorf("result = %+v, want unchecked", r)
	}
}

// A record that fails the strict SOQL charset never reaches the runner and
// renders unchecked. This is the injection guard.
func TestProbeSOQLInjectionGuard(t *testing.T) {
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "001XX0'); DROP TABLE",
		Fields: map[string]string{"object": "Account", "record": "001XX0'); DROP TABLE"},
	}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX0'); DROP TABLE"]; !r.Unchecked {
		t.Errorf("result = %+v, want unchecked for a record that fails the charset", r)
	}
	if called {
		t.Error("runner was called with an unvalidated record; the guard must run first")
	}
}

// A record the query did not return is unchecked, never a false unchanged.
func TestProbeMissingRecordUnchecked(t *testing.T) {
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	r := out["salesforce:001XX000003DHPhYAO"]
	if !r.Unchecked || r.LastActivity != nil {
		t.Errorf("result = %+v, want unchecked with no last activity", r)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nix develop --command go test ./internal/probe/salesforce/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Implement the prober**

Create `internal/probe/salesforce/salesforce.go`:

```go
// Package salesforce probes Salesforce record freshness through the already
// authenticated `sf` CLI. Records are grouped by sObject, one `sf data query`
// runs per group, and each record's LastModifiedDate becomes its last activity
// time. Auth lives entirely in the CLI's own store, so this package holds no
// token. Any CLI, status, or query failure renders the affected links unchecked,
// never a false unchanged.
package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// sfTimeLayout is Salesforce's LastModifiedDate format, e.g.
// 2026-07-20T09:00:00.000+0000.
const sfTimeLayout = "2006-01-02T15:04:05.000-0700"

// prefixObject maps a Salesforce 3-char id key prefix to its sObject, used when
// a record arrives without an object hint from its URL.
var prefixObject = map[string]string{
	"001": "Account", "003": "Contact", "005": "User",
	"006": "Opportunity", "00Q": "Lead", "500": "Case",
	"701": "Campaign", "800": "Contract",
}

var (
	// sfIDCharset admits only a bare 15 or 18 char Salesforce id.
	sfIDCharset = regexp.MustCompile(`^[0-9A-Za-z]{15,18}$`)
	// sfCaseCharset admits only a numeric Case number.
	sfCaseCharset = regexp.MustCompile(`^[0-9]+$`)
	// sfObjectCharset admits only a valid sObject API name.
	sfObjectCharset = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Runner runs a command and returns its stdout. `sf ... --json` writes its
// envelope to stdout even on a non-zero exit, so callers parse stdout rather
// than relying on the error alone.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client is the Salesforce freshness prober.
type Client struct {
	runner  Runner
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithRunner injects a command runner, so tests drive the prober with recorded
// envelopes and no CLI.
func WithRunner(r Runner) Option { return func(c *Client) { c.runner = r } }

// New builds a Salesforce prober. The default runner shells out to `sf` and the
// limiter is a conservative 5 queries per second, one query per sObject group.
func New(opts ...Option) *Client {
	c := &Client{
		runner:  defaultRunner,
		limiter: rate.NewLimiter(rate.Limit(5), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Available reports whether the `sf` binary is on PATH, so the composition root
// registers this prober only when it can actually run.
func Available() bool {
	_, err := exec.LookPath("sf")
	return err == nil
}

// defaultRunner runs `sf` and captures stdout, returning it alongside any exit
// error so the caller can still parse the JSON envelope on a non-zero status.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.Bytes(), err
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemSalesforce }

// sfEnvelope is the subset of an `sf ... --json` response this prober decodes.
// status is 0 on success and non-zero on failure, where name and message carry
// the error.
type sfEnvelope struct {
	Status  int    `json:"status"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Result  struct {
		Records []sfRecord `json:"records"`
	} `json:"result"`
}

type sfRecord struct {
	Attributes struct {
		Type string `json:"type"`
	} `json:"attributes"`
	ID               string `json:"Id"`
	CaseNumber       string `json:"CaseNumber"`
	LastModifiedDate string `json:"LastModifiedDate"`
}

// Probe types each link to its sObject, groups id lookups by object and Case
// numbers on their own, and runs one query per group. The incoming watermark is
// unused: each query returns an absolute LastModifiedDate and the engine
// compares it against the work-log baseline.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))

	idsByObject := map[string][]links.Link{} // object -> id links
	var caseNumbers []links.Link              // all-digit Case numbers

	for _, l := range ls {
		rec := l.Record
		if rec == "" {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
			continue
		}
		if sfCaseCharset.MatchString(rec) {
			caseNumbers = append(caseNumbers, l)
			continue
		}
		if !sfIDCharset.MatchString(rec) {
			// The record reached us but is neither a clean id nor a Case number,
			// so it cannot safely enter a SOQL string.
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			continue
		}
		object := l.Fields["object"]
		if object == "" {
			mapped, ok := prefixObject[rec[:3]]
			if !ok {
				out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonNoProbe}
				continue
			}
			object = mapped
		}
		if !sfObjectCharset.MatchString(object) {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			continue
		}
		idsByObject[object] = append(idsByObject[object], l)
	}

	for object, group := range idsByObject {
		c.queryIDs(ctx, object, group, out)
	}
	if len(caseNumbers) > 0 {
		c.queryCases(ctx, caseNumbers, out)
	}
	return out, nil
}

// queryIDs runs one `SELECT Id, LastModifiedDate FROM <object> WHERE Id IN (...)`
// and maps each returned record back to its link by id.
func (c *Client) queryIDs(ctx context.Context, object string, group []links.Link, out map[string]probe.Result) {
	ids := make([]string, len(group))
	for i, l := range group {
		ids[i] = l.Record
	}
	soql := "SELECT Id, LastModifiedDate FROM " + object + " WHERE Id IN (" + quoteList(ids) + ")"

	env, reason, ok := c.runQuery(ctx, soql)
	if !ok {
		markGroup(group, reason, out)
		return
	}
	byID := map[string]sfRecord{}
	for _, r := range env.Result.Records {
		byID[r.ID] = r
	}
	for _, l := range group {
		out[l.Key()] = resultFor(byID[l.Record], byID, l.Record)
	}
}

// queryCases runs one `SELECT CaseNumber, LastModifiedDate FROM Case WHERE
// CaseNumber IN (...)` and maps each returned record back to its link by number.
func (c *Client) queryCases(ctx context.Context, group []links.Link, out map[string]probe.Result) {
	nums := make([]string, len(group))
	for i, l := range group {
		nums[i] = l.Record
	}
	soql := "SELECT CaseNumber, LastModifiedDate FROM Case WHERE CaseNumber IN (" + quoteList(nums) + ")"

	env, reason, ok := c.runQuery(ctx, soql)
	if !ok {
		markGroup(group, reason, out)
		return
	}
	byNum := map[string]sfRecord{}
	for _, r := range env.Result.Records {
		byNum[r.CaseNumber] = r
	}
	for _, l := range group {
		r, seen := byNum[l.Record]
		if !seen {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			continue
		}
		out[l.Key()] = parseActivity(r.LastModifiedDate)
	}
}

// resultFor turns a looked-up record into a Result, unchecked when the query did
// not return it.
func resultFor(r sfRecord, byID map[string]sfRecord, id string) probe.Result {
	if _, seen := byID[id]; !seen {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return parseActivity(r.LastModifiedDate)
}

// parseActivity parses a Salesforce timestamp, unchecked when it will not parse.
func parseActivity(raw string) probe.Result {
	t, err := time.Parse(sfTimeLayout, raw)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &t}
}

// markGroup renders every link in a failed query's group unchecked.
func markGroup(group []links.Link, reason probe.Reason, out map[string]probe.Result) {
	for _, l := range group {
		out[l.Key()] = probe.Result{Unchecked: true, Reason: reason}
	}
}

// runQuery runs one SOQL through the CLI and classifies the outcome. ok is false
// on any failure, with the reason to render. `sf --json` writes its envelope even
// on a non-zero exit, so the envelope status is authoritative over the exit code.
func (c *Client) runQuery(ctx context.Context, soql string) (sfEnvelope, probe.Reason, bool) {
	if err := c.limiter.Wait(ctx); err != nil {
		return sfEnvelope{}, probe.ReasonFromCtx(ctx), false
	}
	stdout, runErr := c.runner(ctx, "sf", "data", "query", "--query", soql, "--json")

	var env sfEnvelope
	if err := json.NewDecoder(io.LimitReader(bytes.NewReader(stdout), probe.MaxResponseBytes)).Decode(&env); err != nil {
		// No parseable envelope: the binary failed to run or produced garbage.
		_ = runErr
		return sfEnvelope{}, probe.ReasonFromCtx(ctx), false
	}
	if env.Status != 0 {
		if isAuthFailure(env.Name, env.Message) {
			return env, probe.ReasonAuth, false
		}
		return env, probe.ReasonError, false
	}
	return env, "", true
}

// isAuthFailure reports whether an `sf` error names a missing or expired org
// authorization rather than a query problem.
func isAuthFailure(name, message string) bool {
	hay := strings.ToLower(name + " " + message)
	for _, kw := range []string{"authorization", "authenticate", "no org", "not been authorized", "expired", "session", "logged in", "login"} {
		if strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}

// quoteList renders ids as a SOQL value list: 'a','b'. Every id is charset
// validated before it reaches this point, so no escaping is required.
func quoteList(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "'" + id + "'"
	}
	return strings.Join(quoted, ",")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/probe/salesforce/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/probe/salesforce/
git commit -m "feat(probe): add Salesforce prober over the sf CLI"
```

---

## Task 3: Registration wiring

**Files:**
- Modify: `internal/probe/probeset/probeset.go`
- Modify: `internal/cli/probe.go`
- Test: `internal/probe/probeset/probeset_test.go`

- [ ] **Step 1: Write the failing registration test**

Add to `internal/probe/probeset/probeset_test.go`:

```go
// The Salesforce prober registers on the availability flag, not a token: its
// auth lives in the sf CLI's own store.
func TestBuildSalesforceOnAvailability(t *testing.T) {
	reg := Build(Credentials{Salesforce: true})
	if _, ok := reg.For(links.SystemSalesforce); !ok {
		t.Error("salesforce prober not registered despite Salesforce=true")
	}

	reg2 := Build(Credentials{})
	if _, ok := reg2.For(links.SystemSalesforce); ok {
		t.Error("salesforce prober registered when Salesforce=false")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/probe/probeset/ -run TestBuildSalesforce`
Expected: FAIL — `Credentials` has no `Salesforce` field.

- [ ] **Step 3: Register the prober**

In `internal/probe/probeset/probeset.go`, add the import:

```go
	"github.com/bashfulrobot/ballpoint/internal/probe/salesforce"
```

Add a field to `Credentials` (after `Google`):

```go
	// Salesforce is true when the sf CLI is available. Salesforce auth lives in
	// the CLI's own store, not this off-store secrets file, so there is no token.
	Salesforce bool
```

In `Build`, before `return reg`:

```go
	if c.Salesforce {
		reg.Register(salesforce.New())
	}
```

- [ ] **Step 4: Run the probeset suite to verify it passes**

Run: `nix develop --command go test ./internal/probe/probeset/`
Expected: PASS.

- [ ] **Step 5: Wire availability into `resolveProbeDeps`**

In `internal/cli/probe.go`, add the import:

```go
	"github.com/bashfulrobot/ballpoint/internal/probe/salesforce"
```

In `resolveProbeDeps`, after the `deps.creds.Google` line:

```go
	deps.creds.Salesforce = salesforce.Available()
```

- [ ] **Step 6: Run the cli suite to verify nothing regressed**

Run: `nix develop --command go test ./internal/cli/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/probe/probeset/ internal/cli/probe.go
git commit -m "feat(probe): register the Salesforce prober when the sf CLI is available"
```

---

## Task 4: README and full verification

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the probers prose**

In `README.md`, in the `### Probers and unchecked sources` section:

Change `Four systems ship a prober: Slack, Gmail, Aha, and Drive.` to `Five systems ship a prober: Slack, Gmail, Aha, Drive, and Salesforce.`

Change the sentence `Jira, Salesforce, and GitHub have `no probe available` in this release (Salesforce is tracked separately as #10).` to `Jira and GitHub have `no probe available` in this release.`

After the paragraph that ends `references to probe this run`, add:

```markdown
Salesforce is the one prober that does not talk HTTP. It reuses the already
authenticated `sf` CLI (`sf data query ... --json`), so the auth story stays out
of ballpoint entirely, there is no `salesforce_token` key, and nothing here reads
a Salesforce credential. Records are grouped by sObject, one query runs per group,
and each record's `LastModifiedDate` is its last activity time. The sObject comes
from a Lightning URL's object segment when present, otherwise from the id's 3-char
key prefix; an all-digit reference queries `Case` by `CaseNumber`. An unmapped
prefix, a missing or unauthenticated CLI, a non-zero `sf` status, or a query error
renders the affected links `unchecked`, never a false unchanged.
```

- [ ] **Step 2: Update the Secrets section**

In `README.md`, in the `## Secrets` section, after the paragraph ending `That source's links render `unchecked` for the run instead of failing it.`, add:

```markdown
Salesforce is the exception to the per-source token pattern. Its auth lives in
the `sf` CLI's own store, not this secrets file, so there is no `salesforce_token`
key. The prober is registered when the `sf` binary is on PATH; when it is absent,
Salesforce links render `unchecked` like any other unregistered source.
```

- [ ] **Step 3: Verify the README claims match the code**

Run: `grep -n "salesforce_token" README.md`
Expected: only the two mentions above, both stating the key does not exist.

Run: `grep -n "no probe available" README.md`
Expected: the remaining mention lists only Jira and GitHub.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: promote Salesforce to a shipped prober and document the CLI-auth model"
```

- [ ] **Step 5: Full verification suite**

Run:
```bash
nix develop --command go test ./internal/probe/salesforce/ ./internal/links/
nix develop --command go test ./...
nix develop --command golangci-lint run
nix flake check
```
Expected: all PASS. No credential value appears in any test or log.

---

## Self-Review

- **Spec coverage:** Each acceptance-criteria checkbox maps to a task. `probe.Prober`/`System()` (Task 2); injectable runner + arg shape (Task 2, `TestProbeCaseByNumber`); host + URL parsing (Task 1); bare-id-inside-URL fix (Task 1, `TestExtractSalesforceLightningURL`); object typing from URL vs prefix, all-digit Case, unknown-prefix unchecked (Task 2); `LastModifiedDate` parse (Task 2); charset guard (Task 2, `TestProbeSOQLInjectionGuard`); unchecked invariant with `ReasonAuth` vs `ReasonError` (Task 2); README + no `salesforce_token` (Task 4); test matrix (Task 2). Registration (Task 3).
- **Placeholder scan:** No TBD/TODO; every code step carries complete code.
- **Type consistency:** `Runner`, `Client`, `New`, `WithRunner`, `Available`, `System`, `Probe`, `sfEnvelope`, `sfRecord`, `prefixObject`, `Credentials.Salesforce` are named identically across tasks. The watermark key format `salesforce:<record>` matches `links.Link.Key()`.
