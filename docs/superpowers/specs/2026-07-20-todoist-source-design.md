# Todoist source design

Design for issue #2: a `Source` interface, a direct Todoist HTTP client, and a
watermark plus cache store. This is the data layer the freshness probe (#3)
and everything downstream reads.

## Problem

Every Todoist read in the triage path shells out to the `td` CLI. Against a
live 71 task scope a single `td` call costs 470 to 1050 ms, dominated by Node
startup rather than network. Fetching a task plus its comments takes 1.9 s
because the two independent calls run one after the other.

Parallelising the existing CLI already helps: 8 tasks sequentially take 7.5 s,
in parallel 0.88 s, and a full 71 task prefetch at 12 way concurrency lands at
15.3 s with no rate limiting. Most of that remaining time is still Node
startup times 142. Talking HTTP directly removes the Node cost entirely.

## Scope

In scope: the `Source` interface, the Todoist HTTP client, the runtime secret
loader, and the watermark plus cache store.

Out of scope: any non Todoist source (#3 onward). Replacing the `td_*.sh`
mutation macros, which are tested and working, and which cover the write path
rather than the slow read path.

## Architecture

Four new packages, each with one job.

### `internal/secrets`

Reads a single value from the off-store secrets file at runtime. This mirrors
`modules/apps/cli/aha-fr-report/default.nix`, which reads its token from
`~/.config/nixos-secrets/secrets.json` inside the script rather than through
`home.sessionVariables`, because systemd user services do not inherit session
variables. Any binary that later runs from a timer has the same constraint,
which is why the token cannot come from an environment variable or the Nix
store.

```go
// Load reads a flat top-level string key from the JSON secrets file at path.
func Load(path, key string) (string, error)

// DefaultPath returns ~/.config/nixos-secrets/secrets.json.
func DefaultPath() (string, error)
```

The Todoist token is the flat key `todoist_token`. Load returns a clear error
when the file is missing or unreadable and a distinct error when the key is
absent or empty, matching the two failure messages the aha script prints. The
value is returned to the caller and never logged. A future source that needs a
different key calls Load with that key, so the package generalises without
change.

### `internal/sources`

The interface every system implements, plus the shared value types.

```go
// Watermark records the last seen activity time per link key. A link key
// identifies one task's relationship to one external system. For a Todoist
// only world that is the task itself, keyed "todoist:<taskID>".
type Watermark map[string]time.Time

// Task is the normalised shape every source returns, independent of any one
// API's field names.
type Task struct {
	ID          string
	Title       string
	Project     string    // resolved name, not the raw project_id
	Section     string    // resolved name, empty when none
	Due         string    // date or natural language string, empty when none
	Recurring   bool
	Priority    string    // always p1 through p4, never the raw API integer
	Labels      []string
	Description string
	URL         string
	UpdatedAt   time.Time // the watermark source
	Comments    []Comment
}

type Comment struct {
	ID         string
	Content    string
	PostedAt   time.Time
	Attachment string // file name, empty when none
}

// Delta is what a probe returns: the tasks it fetched, the link keys whose
// activity time moved past the incoming watermark, and the watermark to
// persist for next time.
type Delta struct {
	Tasks   []Task
	Changed []string  // link keys that changed since the incoming watermark
	Next    Watermark
}

// Source is one external system. Adding a system means adding one package that
// implements this and nothing else.
type Source interface {
	Name() string
	Probe(ctx context.Context, since Watermark) (Delta, error)
}
```

Priority normalisation lives at the client boundary, not here, so no `Task`
ever carries a raw integer. The `Priority` field is typed as a plain string
holding `p1` through `p4`.

### `internal/sources/todoist`

The HTTP client. Talks to `https://api.todoist.com/api/v1` with
`Authorization: Bearer <token>` and a `User-Agent` of
`ballpoint/<version> (+https://github.com/bashfulrobot/ballpoint)`, where the
version is passed to the constructor rather than imported from `buildinfo`, so
the client stays testable and the base URL is injectable for the mock server.

```go
type Client struct { /* baseURL, http.Client, token, userAgent, limit, limiter */ }

func New(token string, opts ...Option) *Client   // Option sets version, concurrency, baseURL, http client
func (c *Client) Name() string                    // "todoist"
func (c *Client) Probe(ctx, since) (Delta, error)
```

Probe does, in order:

1. One paginated `GET /tasks` over the scope, following `next_cursor` until it
   is empty, accumulating the raw task list. Pagination is `{results,
   next_cursor}` with a `limit` and `cursor` query.
2. `GET /projects` and `GET /sections` once each, built into id to name maps
   so tasks carry resolved names.
3. For each task, in parallel under a bounded group, `GET /comments?task_id=`.
   This is the 71 comment calls that, with the single task list and the two
   name lists, replace the reference's 142 sequential `td` invocations.
4. Normalisation: priority becomes `p` + `(5 - api)`, so API 4 (highest)
   becomes `p1`; project and section ids resolve to names; `updated_at`
   becomes the watermark time, falling back to `added_at` when a task has
   never been updated.
5. Delta assembly: `Changed` holds every link key whose `updated_at` is after
   the incoming watermark's entry (or absent from it), and `Next` records the
   current `updated_at` for every task.

Todoist v1 uses snake_case field names (`project_id`, `section_id`,
`is_recurring`, `added_at`, `updated_at`). The public reference truncates
before the full schema, so the client decodes only the fields it needs and the
golden tests pin the decode against recorded fixtures. The live fetch is the
final check on field names.

Bounded concurrency is `errgroup.WithContext` plus `SetLimit(n)`, with `n` an
option defaulting to 12. A `golang.org/x/time/rate` limiter is a second layer
guarding the API's published rate limit, so a large scope cannot burst. Both
are constructor options with defaults, so the common caller sets neither.

### `internal/store`

Persists watermarks and cached payloads under `config.StateDir()`, which issue
#1 resolved to `$XDG_STATE_HOME/ballpoint`.

- `watermarks.json` maps a link key to an RFC3339 timestamp. This is the "keyed
  by link" half.
- `cache/<taskID>.json` holds the last fetched `Task` (with its comments).
  This is the "keyed by task" half.

Watermarks store timestamps, not content hashes, so #3 can decide whether a
fetch is needed with a timestamp compare before paying for any HTTP. Every
write is atomic: write a temp file in the same directory, then rename over the
target, so a killed timer never leaves a torn file.

```go
type Store struct { root string }

func Open(root string) (*Store, error)                 // root is config.StateDir()
func (s *Store) LoadWatermark() (sources.Watermark, error)
func (s *Store) SaveWatermark(w sources.Watermark) error
func (s *Store) LoadTask(id string) (sources.Task, bool, error)
func (s *Store) SaveTask(t sources.Task) error
```

A missing watermark file is not an error: it returns an empty watermark, so the
first run fetches everything.

## Data flow

```
secrets.Load(path, "todoist_token") ─▶ token
                                         │
config.StateDir() ─▶ store.Open ─▶ store.LoadWatermark ─▶ since
                                         │                  │
                                         ▼                  ▼
                          todoist.New(token, opts).Probe(ctx, since)
                                         │
                            ┌────────────┼─────────────┐
                       GET /tasks   GET /projects   GET /sections
                       (paginated)      │                │
                            └──── per task: GET /comments?task_id= (bounded) ──┘
                                         │
                              normalise + assemble Delta
                                         │
                       store.SaveTask(each) ; store.SaveWatermark(Delta.Next)
```

The probe engine in #3 owns this wiring. Issue #2 ships the pieces and a thin
seam to exercise them end to end under test.

## Benchmark

The engineering claim is that bounded concurrency removes the sequential cost.
Two ways to show it, because a live figure needs the real token and network
that an autonomous run and CI cannot supply and must never read.

- Reproducible, in CI, no secret: a `httptest.Server` that sleeps a fixed
  delay per request stands in for the API. A benchmark fetches a 71 task scope
  through the real client against it, once sequentially (limit 1) and once at
  the default 12, and asserts the concurrent run is at least several times
  faster. The wall clock figure is printed and recorded in the PR. This proves
  the mechanism deterministically.
- The real figure, one command, run by the user: `ballpoint probe --benchmark`
  (wired minimally here, filled in by #3) loads the token the normal way, runs
  one real prefetch, and prints the wall clock against the 15.3 s baseline. The
  PR documents the command. The token never enters this session.

The acceptance criterion's live figure is produced by that command. The PR
records the mock figure now and the command to produce the live one.

## Error handling

Every HTTP call threads `ctx`. A non 2xx response returns an error naming the
endpoint and status, never the token. A cancelled context aborts the bounded
group and Probe returns the group's error. `secrets.Load` distinguishes a
missing file from a missing key. Store operations wrap I/O errors with the
path.

## Testing

Table driven, golden files under `testdata/`, following the convention issue #1
set with `internal/golden`.

- Priority normalisation: API 1 through 4 map to p4 through p1, verified as a
  table.
- Watermark round trip: save then load returns an equal map, and a missing file
  loads empty.
- Pagination: a mock server returning two pages with a `next_cursor` is drained
  to a single accumulated list, and a golden file pins the normalised result.
- Secret loading: a temp secrets file with and without the key, and a missing
  file, each produce the right value or the right distinct error, with no value
  in any message.
- The concurrency benchmark doubles as a correctness test that the bounded
  fetch returns every task's comments.

## Acceptance criteria mapping

| Criterion | Where |
| --- | --- |
| Source interface with a probe taking ctx and a watermark, returning a delta and error | `internal/sources` |
| Todoist package fetches tasks, comments, projects, sections over HTTP, no `td` | `internal/sources/todoist` |
| Token from off-store secrets file, never env or store | `internal/secrets` |
| Priority normalised to p1 through p4 on read | client boundary in `todoist` |
| Store persists watermarks and cache under the state dir, by task and by link | `internal/store` |
| Bounded concurrency configurable, default 12 | `todoist` client option |
| 71 task fetch beats 15.3 s, figure in the PR | benchmark, mock figure now plus live command |
| Table driven tests for priority, watermark round trip, pagination, golden files | `_test.go` plus `testdata/` |

## Risks

The v1 field names are pinned against recorded fixtures rather than a complete
published schema, so a field Todoist renames breaks the live fetch until the
struct tag is updated. The golden tests will still pass, so the live command is
the real check. The client isolates every field name in one decode struct, so
the fix is one place.

The mock benchmark proves the concurrency mechanism, not the absolute 15.3 s
number, which depends on the user's live scope and network. The PR is explicit
about which figure is which.
