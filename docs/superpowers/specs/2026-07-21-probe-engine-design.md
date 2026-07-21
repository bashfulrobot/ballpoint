# Probe engine design

Design for issue #3: a batch-by-system freshness probe engine. It answers, per
task, what changed at each linked system since the last recorded work-log
entry, deterministically and with no model involvement. This is the read-only
freshness layer that the dispatcher (#6) and the triage walk (#5) consume.

## Problem

A triage card is trustworthy only if the task is current with every system it
references. The check rarely runs today because it is framed per link, which
looks expensive. Measured against the real corpus (71 tasks, 148 links), it is
cheap when batched by system rather than by link: roughly 46 calls instead of
148. Slack carries most of the collapse, because one channel history call
reports the freshness of every thread in that channel at once, and 16 tasks
reference the same channel.

Two facts shape the output. A source that cannot be checked must render as
`unchecked`, never as `no change`, because a silent false negative on freshness
is worse than no probe at all. And 33 of 71 tasks carry no comments, so for
nearly half the backlog there is no work log to compare against.

## Scope

In scope: the probe engine, per-source freshness clients, per-source rate
limiting, watermark reconciliation, and the JSON output.

Out of scope: interpreting what a change means (the dispatcher, #6); any write
to Todoist (this issue is strictly read only); rendering (the consumers come
later).

### Which sources ship a prober

The acceptance criteria name four probers to build: Slack (the load-bearing
channel-history collapse), Gmail, Aha, and Drive (each a single changed-since
query). Teams renders `unchecked` with a not-probeable reason. Jira,
Salesforce, and GitHub appear only in the context table, not in the acceptance
criteria, so this issue renders them `unchecked` with a no-probe reason rather
than shipping half-built clients. The engine treats a missing prober as a
first-class `unchecked` outcome (see the invariant below), so adding one of
those later is adding one package that registers itself, with no engine change.
This is a deliberate scope decision, recorded as an autonomous decision on the
PR.

## Architecture

Two new packages plus the engine, layered on the #2 foundation.

```
internal/links     extract and categorise links from a task, parse permalinks
internal/probe     the engine: group by system, fan out, reconcile watermarks,
                   emit JSON; plus one sub-package per source prober
```

### `internal/links`

Turns a task into the set of external references it carries.

```go
// System is the canonical name of an external system.
type System string

const (
	SystemSlack  System = "slack"
	SystemGmail  System = "gmail"
	SystemAha    System = "aha"
	SystemGDrive System = "gdrive"
	SystemJira   System = "jira"
	SystemTeams  System = "teams"
	SystemZoom   System = "zoom"
	// ... plus SystemGitHub, SystemSalesforce, SystemConfluence, SystemURL
)
```

A Google Doc is a Drive file, so for freshness both `docs.google.com` and
`drive.google.com` links carry a file id and resolve through the Drive files
API. Extraction categorises both as `SystemGDrive` with the file id as
`Record`, collapsing the reference's separate `gdocs` bucket, because one Drive
prober checks both. The `gdocs` provenance is not needed once the file id is
parsed.

```go

// Link is one reference from a task to an external record. Record is the
// stable per-record identity within System; it is the watermark key suffix.
type Link struct {
	System  System
	Raw     string            // the URL or bare id as it appeared
	Record  string            // parsed record identity, e.g. "<channel>:<ts>"
	Fields  map[string]string // parsed parts, e.g. channel, thread, fileID
}

// Key is the watermark key: "<system>:<record>". Stable across runs.
func (l Link) Key() string { return string(l.System) + ":" + l.Record }

// Extract scans a task's title and every comment body, harvests URLs and bare
// identifiers, categorises each, and returns the deduplicated links in
// first-seen order.
func Extract(task sources.Task) []Link
```

Extraction ports `lib_extract.sh`: harvest `https?://[^ )<>"']+`, strip trailing
`.,;:!?`, categorise by host substring, and match the bare-id regexes (Aha
`[A-Z]{3,5}-I-[0-9]+`, Jira `[A-Z]{2,6}-[0-9]+` minus `-I-`, Salesforce
`00[0-9A-Za-z]{13}([0-9A-Za-z]{3})?`, `Case [0-9]{5,}`). Categorisation is a
host table matching the reference: `*slack.com*`, `*teams.microsoft.com*`,
`*mail.google.com*`, `*docs.google.com*`, `*.aha.io*`, `*atlassian.net*`,
`*confluence*`, `*zoom.us*`, `*app.todoist.com*`, else `url`.

Per-system permalink parsing is new. The parser for each system fills `Record`
and `Fields`, and a link whose record cannot be parsed keeps `Record` empty and
is reported `unchecked` with an unparseable reason rather than dropped.

- Slack: `https://<ws>.slack.com/archives/<CHANNEL>/p<TS>[/...]`. `p1234567890123456`
  reconstitutes the thread ts `1234567890.123456` (insert the decimal six
  digits from the end). `Record` is `<CHANNEL>:<ts>`, `Fields` has `channel`
  and `thread`. A `thread_ts` query parameter, when present, overrides the path
  ts (a reply permalink).
- Gmail: `https://mail.google.com/mail/u/<n>/#<label>/<threadHex>`. `Record` is
  the trailing hex thread id.
- Aha: `https://<ws>.aha.io/features/<KEY>` or a bare `KEY-I-N`. `Record` is the
  reference key.
- Drive: `https://(drive|docs).google.com/.../d/<FILE_ID>/...`, categorised
  `SystemGDrive`. `Record` is the file id.

### `internal/probe`: the engine

```go
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

// Freshness is the engine's verdict for one link.
type Freshness struct {
	Key          string     // links.Link.Key()
	LastActivity *time.Time // last activity at the system, nil when unknown
	Changed      bool       // activity is newer than the task's last log
	Unchecked    bool       // true when the system could not be checked
	Reason       Reason     // set only when Unchecked
}

// Prober checks one system. It receives every link for its system across all
// tasks at once, so it can batch (Slack collapses to one call per channel).
// since is the incoming watermark. It returns the last activity time per link
// key. The engine, not the prober, decides Changed and writes watermarks.
type Prober interface {
	System() links.System
	Probe(ctx context.Context, ls []links.Link, since sources.Watermark) (map[string]ProbeResult, error)
}

// ProbeResult is a prober's per-link finding: the last activity time, or an
// unchecked reason. A prober that returns neither for a key it was given is
// treated by the engine as unchecked with ReasonError.
type ProbeResult struct {
	LastActivity *time.Time
	Unchecked    bool
	Reason       Reason
}
```

The engine's `Run`:

1. Takes the fetched tasks (from the #2 Todoist source), extracts links per
   task, and indexes every distinct link by system.
2. For each system with a registered prober, calls `Probe` once with that
   system's whole link set and the incoming watermark. Systems with no prober,
   and Teams, skip the call and mark every link `unchecked` with the fitting
   reason.
3. Reconciles: for each link, the task's last logged time is the newest comment
   `PostedAt` on that task. `Changed` is `LastActivity.After(lastLogged)`. A
   task with zero comments has no work log, so it reports `has_work_log:false`
   and the baseline falls back to the task's own `UpdatedAt`, so `Changed` stays
   computable and a link is never dropped. The task-level flag signals that the
   baseline is the task itself, not a work log.
4. Writes the next watermark as `LastActivity` per link key, but only for links
   that were actually checked, so an `unchecked` link never overwrites a good
   prior watermark with a zero. Persist through the #2 store atomically.
5. Emits JSON keyed by task.

### The unchecked invariant

This is the engine's core guarantee, enforced in one place. A prober that
returns an error, exceeds its deadline, is absent, or omits a key it was asked
about causes every affected link to render `unchecked` with a reason, never
`Changed=false`. The engine never lets a failure masquerade as freshness. The
invariant is unit-tested directly: a prober rigged to error, one rigged to
time out, and a system with no prober each yield `unchecked`, and none writes a
watermark.

## The Slack collapse

The load-bearing piece, built and proven first.

1. Group the Slack links by channel. For each distinct channel, one
   `conversations.history` call returns the channel's recent messages. Each
   thread parent carries `latest_reply` (the ts of its newest reply) and
   `reply_count`.
2. For each Slack link (a channel plus a thread ts), read `latest_reply` from
   the history response. If it is newer than the stored watermark for that
   link key, the thread moved; otherwise it did not, with no further call.
3. Only for threads whose `latest_reply` advanced, issue one
   `conversations.replies` call to read the new activity time precisely.

So 40 channel calls plus a handful of replies calls cover 105 threads, versus
105 calls one per thread. The channel history responses are the batch; the
replies calls are the tail.

Slack limits the relevant methods to roughly 50 requests per minute (Tier 3),
so the per-source limiter is sized to `rate.Every(time.Minute/50)` with a small
burst. An expired or missing Slack token makes the whole Slack prober return
`unchecked` for every Slack link (`ReasonAuth`), never `no change`.

## Per-source rate limiting

One `golang.org/x/time/rate.Limiter` per source, sized to that vendor's
documented limit, declared where the prober is constructed:

- Slack Tier 3: 50 per minute.
- Gmail API: a generous per-user quota, so a conservative 10 per second guards it.
- Aha: a conservative 5 per second, well under the documented ceiling.
- Drive: 10 per second per user is well within quota.

These are guards, not tuned throughput. Each is a constructor value so a caller
can override it, and the exact ceilings are confirmed by the live run, the same
way #2 left the Todoist ceiling to its live command.

## Credentials

Every source reads its credential from the off-store secrets file at runtime
through `internal/secrets`, never an environment variable or the Nix store,
because the probe runs from a timer (#4). One flat key per source: `slack_token`,
`aha_token`, and the Google credential the Gmail and Drive probers share. A
missing or malformed credential is not a hard failure of the whole run; it makes
that one source return `unchecked` for its links, so a run with a dead Slack
token still reports Gmail and Aha.

## Data flow

```
todoist.Client.Probe ─▶ tasks (+ comments = work log)
                              │
              links.Extract per task ─▶ links grouped by system
                              │
store.LoadWatermark ─▶ since  │
                              ▼
        for each system: registry.Prober(system).Probe(ctx, links, since)
              (Slack: 1 history/channel, replies only for advanced threads)
                              │
             reconcile: Changed = LastActivity.After(newest comment PostedAt)
                              │
        store.SaveWatermark(next, checked links only)  ;  emit JSON by task
```

## JSON output

Machine readable, keyed by task id, stable field order.

```json
{
  "generated_at": "2026-07-21T09:00:00Z",
  "tasks": {
    "8899": {
      "title": "Follow up with platform team",
      "has_work_log": true,
      "last_logged": "2026-07-18T14:00:00Z",
      "links": [
        {
          "key": "slack:C1:1699999999.000100",
          "system": "slack",
          "raw": "https://x.slack.com/archives/C1/p1699999999000100",
          "last_activity": "2026-07-20T09:00:00Z",
          "changed": true
        },
        {
          "key": "teams:...",
          "system": "teams",
          "raw": "https://teams.microsoft.com/l/message/...",
          "unchecked": true,
          "reason": "not probeable"
        }
      ]
    },
    "9001": {
      "title": "Draft the brief",
      "has_work_log": false,
      "links": []
    }
  }
}
```

`has_work_log` is false when the task has zero comments. `last_logged` is
omitted there, and `changed` is measured against the task's `updated_at`
instead. `changed` and `unchecked` are mutually exclusive per link.

## Watermark reconciliation

Watermarks extend the #2 store unchanged. The key space grows from
`todoist:<taskID>` to include per-record keys like `slack:<channel>:<ts>`,
`gmail:<threadId>`, `aha:<key>`, `gdrive:<fileId>`. The map type, the atomic
write, and the corrupt-file self-heal are all reused as is.

The run is idempotent and safe to interrupt. Watermarks are written once at the
end through the store's atomic temp-file-and-rename, so an interrupted run
leaves the previous watermark intact and the next run simply re-probes. Only
checked links update their watermark, so an interruption or an `unchecked`
outcome never advances a watermark past activity that was not confirmed. A warm
run reads the persisted watermarks and, for Slack, skips the replies call for
every thread whose `latest_reply` has not advanced, which is what makes the
second run cheaper than the first.

## Error handling

Every probe threads `ctx`, and the engine gives the whole run a bounded
deadline the same way #2's `Probe` does. A prober's own per-request retries and
bounded reads follow the #2 client pattern. A single source failing is isolated
to that source's links by the unchecked invariant. A malformed credential, an
expired token, a non-2xx response, and a timeout all resolve to `unchecked`
with a distinct reason, never to a hard stop or a false `no change`.

## Testing

Table-driven, golden files under `testdata/`, following #2's `internal/golden`
convention driven by `BALLPOINT_UPDATE_GOLDEN`.

- Link extraction and categorisation: a table of task fixtures maps to expected
  links, covering every host in the table, the bare-id regexes, trailing
  punctuation stripping, dedup order, and the Slack ts reconstitution.
- Each prober's response parsing: an `httptest.Server` serving recorded
  fixtures, with a golden file pinning the parsed `ProbeResult` map. Slack gets
  the full path: a channel history fixture with several threads, a watermark
  that leaves some threads unadvanced, and the assertion that only advanced
  threads trigger a replies call (a call-counting handler).
- The unchecked path: a prober that errors, one that times out, one absent, and
  the Teams and no-probe cases, each asserted to yield `unchecked` and to leave
  watermarks untouched.
- The engine end to end: a set of tasks with known links against fake probers,
  golden-pinning the JSON output, including a zero-comment task reported as
  no-work-log.

### Benchmark and live verification

The acceptance criteria include a cold full run within the Slack rate limit
recording its wall clock, and a warm run issuing substantially fewer calls.
Both need live tokens, the network, and the user's own corpus, which an
autonomous run and CI cannot supply and must never read. So, mirroring #2:

- Reproducible in CI, no secret: a call-counting test drives the engine against
  fake probers over a synthetic corpus modelled on the real one (71 tasks, 148
  links, Slack concentrated on ~40 channels) and asserts the batch-by-system
  collapse issues on the order of 46 calls, not 148, and that a warm second run
  issues fewer than the cold run. The figures are printed and recorded in the
  PR. This proves the mechanism deterministically.
- The real figure, one command, run by the user: `ballpoint probe` loads the
  credentials the normal way, runs one real pass, and records its wall clock;
  `ballpoint probe --dry-run` reports what it would call, per system, without
  making the calls, so the user can confirm the warm-versus-cold call counts
  and that a revoked token yields `unchecked`. The PR documents both commands.

## CLI

`ballpoint probe` gains real behaviour behind the existing FlagSet. It resolves
the state dir, opens the store, loads the Todoist token and the per-source
credentials, fetches tasks through the #2 Todoist client, runs the engine, and
writes the JSON to stdout. `--dry-run` runs extraction and grouping and reports
the planned per-system call counts without calling any source or writing any
watermark. `--benchmark` (already parsed) times the real pass and prints the
wall clock. Diagnostics go to stderr, the JSON to stdout, so the command
composes in a pipeline.

## Acceptance criteria mapping

| Criterion | Where |
| --- | --- |
| Probes batch by system, not by link | engine groups by system, one Prober.Probe call per system |
| Slack channel-history collapse, replies only for advanced threads | `internal/probe/slack` |
| Gmail, Aha, Drive single changed-since query | `internal/probe/{gmail,aha,gdrive}` |
| Per-source rate limiting with x/time/rate sized to each vendor | one limiter per prober |
| Error, timeout, or no probe renders unchecked, never no change | the unchecked invariant in the engine |
| Teams renders unchecked with a reason | engine, `ReasonNotProbeable` |
| Watermarks persist between runs | #2 store, extended key space |
| Idempotent and safe to interrupt | end-of-run atomic write, checked-only updates |
| JSON keyed by task, last activity, last logged, changed | engine output |
| Zero-comment tasks reported as no work log | `has_work_log:false` |
| Cold run within Slack rate limit, wall clock recorded | live command plus the CI call-count test |
| Golden tests per source and the unchecked path | `_test.go` plus `testdata/` |

## Risks

The vendor API field names (Slack `latest_reply`/`reply_count`, Gmail history,
Aha updated-since, Drive modifiedTime) are pinned against recorded fixtures
rather than live responses, so a field a vendor renames breaks the live fetch
until the decode struct is updated. Each prober isolates its field names in one
decode struct, so the fix is one place, and the live command is the real check.
The Slack rate limit (roughly 50/min) is the binding constraint on a cold run;
the limiter enforces it, and the call-count test proves the collapse keeps the
cold run within one minute's budget for the measured corpus.
