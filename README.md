# ballpoint

Cross-system task freshness probe, triage TUI, and work dispatcher.

Todoist triage as a program rather than a conversation: a freshness probe that
runs headless on a timer, a keyboard driven triage walk, and a dispatcher that
keeps long jobs from blocking the walk.

## Status

The data layer and the freshness probe are built. The remaining subcommands
are still wired to return `not implemented`.

| Component | Issue | State |
| --- | --- | --- |
| Source interface and Todoist HTTP client | #2 | Done |
| Freshness probe engine | #3 | Done |
| Prewarm timer as a home-manager module | #4 | Wired, not implemented |
| Triage walk TUI | #5 | Wired, not implemented |
| Dispatcher | #6 | Wired, not implemented |

## Usage

```
ballpoint                                  walk the triage queue
ballpoint probe [flags]                    refresh freshness data
ballpoint dispatch                         run queued work
ballpoint --version                        print the build version
```

`probe` flags:

```
--dry-run              report planned per-system calls, no network, no watermark
--benchmark            time one real pass and print the wall clock
--secrets-path PATH    off-store secrets file (default ~/.config/nixos-secrets/secrets.json)
--concurrency N        bounded Todoist fetch concurrency (default 12)
```

The still-unimplemented subcommands exit non-zero, so the systemd timer in
issue #4 cannot record success for work that never ran.

## Sources

A source is one external system ballpoint reads. Each lives in its own package
under `internal/sources` and implements `sources.Source`, a `Name` and a
`Probe(ctx, since)` that returns the fetched tasks, the link keys that changed
since the last run, and the watermark to persist. Adding a system is adding one
package. The only source today is `internal/sources/todoist`, a direct HTTP
client for the Todoist v1 API that replaces shelling out to the `td` CLI on the
read path.

The client fetches the task list once with cursor pagination, resolves project
and section names, and then fetches every task's comments in parallel under a
bounded group (default 12, tunable). Todoist's inverted priority (4 is highest)
is normalised to `p1` through `p4` at the client boundary, so nothing
downstream handles the raw integer.

`internal/store` persists what a probe produces. Watermarks map a link key to
the last activity time in `watermarks.json`, keyed by link. The cache holds the
last fetched task per file in `cache/<taskID>.json`, keyed by task. Every write
lands through a temp file and a rename, so a killed timer never leaves a torn
file. Both live under the state directory described below.

## Probe

`ballpoint probe` answers one question per task: what changed at each linked
system since the last logged work-log entry. It pulls every external reference
out of a task's title and comments, groups those references by system, and runs
one prober per system. The output is JSON keyed by task on stdout, each link
carrying its last activity time and a `changed` flag, or `unchecked` with a
reason.

Reference extraction lives in `internal/links`. It harvests URLs and bare
identifiers, sorts each by host or pattern, and parses a stable record identity
per system: a Slack permalink becomes `slack:<channel>:<ts>` (a reply
permalink's `thread_ts` folds it onto its parent thread), a Gmail thread id, an
Aha reference key, a Drive file id. That record is the watermark key, so the
same thread keys the same way on every run.

### Probers and unchecked sources

Five systems ship a prober: Slack, Gmail, Aha, Drive, and Salesforce. Slack
collapses to one history call per channel. Gmail, Aha, and Drive query each linked
record by id (a thread, an idea, a file) for its absolute last activity time, so a
record the API cannot confirm renders `unchecked` rather than a false unchanged.
The per-system fan-out is capped, and references past the cap render `too many
references to probe this run`, so a task stuffed with links cannot amplify into
an unbounded number of requests.

Salesforce is the one prober that does not talk HTTP. It reuses the already
authenticated `sf` CLI (`sf data query ... --json`), so the auth story stays out
of ballpoint entirely, there is no `salesforce_token` key, and nothing here reads
a Salesforce credential. Records are grouped by sObject, one query runs per group,
and each record's `LastModifiedDate` is its last activity time. The sObject comes
from the id's 3-char key prefix when that prefix is a known standard object, so a
Lightning URL cannot redirect a real id to a different object; the URL's object
segment is the fallback, for a custom object or a standard object outside the
prefix map. An all-digit reference queries `Case` by `CaseNumber`. An unmapped
prefix with no object hint, a missing or unauthenticated CLI, a non-zero `sf`
status, or a query error renders the affected links `unchecked`, never a false
unchanged.

Everything else renders `unchecked` with a reason rather than a freshness
verdict. Teams is `not probeable`. Jira and GitHub have `no probe available` in
this release. A source whose credential is missing or whose token expired is
`credentials missing or expired`.

The unchecked invariant is what the engine guarantees. A prober that errors,
times out, has no registration, or omits a link it was asked about makes every
affected link `unchecked`, never `changed=false`, and never advances that
link's watermark. A silent false negative on freshness is worse than no probe,
so the engine never manufactures a no-change.

### The Slack collapse

Slack is the load-bearing case. One `conversations.history` call per distinct
channel reads each thread's `latest_reply`, and only a thread whose
`latest_reply` moved past the stored watermark costs a `conversations.replies`
call. A batch that mentions 129 Slack threads spread over 40 channels reads 40
history calls plus one reply call per advanced thread, so a warm run is far
cheaper than a cold one. The limiter is sized to Slack's Tier 3 ceiling of
roughly 50 requests per minute.

### Reproducible call-count figure

`TestBatchBySystemCollapsesCalls` drives the engine against counting probers
over a synthetic corpus modelled on the real one: 71 tasks, 148 links, Slack
spread over 40 channels. Batch-by-system issues 3 prober invocations, one per
system, not 148. That proves the collapse with no token and no network.

### Live commands

The live figure needs the real tokens, the network, and the user's own scope,
which an autonomous run and CI cannot supply and must never read.

```
ballpoint probe              # one real pass, JSON report to stdout
ballpoint probe --dry-run    # planned per-system calls, no prober call, no watermark write
ballpoint probe --benchmark  # time the real pass and print the wall clock
```

`--dry-run` still fetches the task list (the input corpus) but makes no prober
call and writes no watermark, so it reports the batch-by-system plan with no
side effects.

## Secrets

The Todoist token is read at runtime from the off-store secrets file at
`~/.config/nixos-secrets/secrets.json`, at the flat top-level key
`todoist_token`. It never comes from an environment variable or the Nix store.
The reason is issue #4: the prewarm timer runs as a systemd user service, and
user services do not inherit session variables. The pattern follows
`modules/apps/cli/aha-fr-report/default.nix` in the nixerator repo, which reads
its token from the same file inside the script for the same reason.
`internal/secrets` is the loader; the value is returned to the caller and never
logged, and no error message includes it.

The probe reads one more flat key per source it can check: `aha_token`. It is
loaded the same way, at runtime, never from the environment or the store, and
never logged. A missing key is not fatal. Aha links render `unchecked` for the
run instead of failing it.

Gmail, Drive, Slack, and Salesforce are the exceptions to the per-source token
pattern. Their auth lives in another tool's store, not this secrets file, so
none has a key here.

Gmail and Drive read the `gws` (Google Workspace CLI) store. Ballpoint exports
the stored credentials through `gws`, exchanges the refresh token for a
short-lived access token each run, and never persists a `google_token`. When
`gws` is absent or unauthenticated, Gmail and Drive links render `unchecked`.

Slack reads the browser-session credentials that `slack-token-refresh` writes to
`~/.config/slack/credentials.json`: an xoxc token and the matching `d` cookie
per workspace. Slack rejects the token without its cookie, so the prober sends
both. It matches each Slack link's workspace host to a stored workspace, with a
single stored workspace used as the fallback. When the store is absent, or holds
no workspace for a link, that link renders `unchecked`. No credential value is
logged or placed in an error.

Salesforce reads the `sf` CLI store, so there is no `salesforce_token` key. The
prober registers when the `sf` binary is on PATH; when it is absent, Salesforce
links render `unchecked` like any other unregistered source.

## Benchmark

The engineering claim is that talking HTTP directly, with the comment fetches
run concurrently, removes the sequential cost of the old `td` prefetch (15.3 s
for a 71 task scope at 12 way concurrency).

`TestConcurrencySpeedup` proves the concurrency mechanism reproducibly, with no
token and no network. It drives the real client against a mock server that
sleeps a fixed delay per request, once sequentially and once at 12 way
concurrency, and asserts the concurrent run is at least four times faster. A
representative local run: sequential 781 ms, 12 way 100 ms, a 7.8x speedup.
This measures the mechanism against a mock, not the live API.

The live figure needs the real token, the network, and the user's own scope,
which an autonomous run and CI cannot supply and must never read. `ballpoint
probe --benchmark` loads the token the normal way, runs one real pass, and
prints the wall clock against the 15.3 s baseline.

## Development

```
nix develop                                  # go, gopls, golangci-lint, delve
nix develop --command go test ./...
nix develop --command golangci-lint run
nix build && ./result/bin/ballpoint --version
```

Golden files under `testdata/` are regenerated by setting an environment
variable. Review the diff before committing.

```
nix develop --command sh -c 'BALLPOINT_UPDATE_GOLDEN=1 go test ./...'
```

The comparison helper lives in `internal/golden`. Regeneration is driven by an
environment variable rather than a `-update` flag because `go test ./...`
hands every flag to every test binary, so a flag registered in one package
makes the command fail in all the others. The variable needs no per-package
registration, so a new test package works without opting in.

`nix build` only builds `cmd/ballpoint`, whose check phase has no tests to
run. `nix flake check` covers the real test suite through `checks.go-test`,
along with the home-manager module.

### After changing dependencies

`nix/ballpoint.nix` pins a `vendorHash` covering the module graph, so any
`go.mod` change invalidates it. Set it to `lib.fakeHash`, run `nix build`, and
copy the hash from the mismatch error.

`internal/tools/tools.go` sits behind a `tools` build tag and holds the only
imports of the Charm libraries, errgroup, and the rate limiter. It looks
unused and is not: deleting it drops those pins on the next `go mod tidy`. CI
compiles it with `go build -tags tools ./...` and lints it with the same tag,
so a broken import there fails rather than passing unnoticed.

`nix/ballpoint.nix` builds from an explicit file allowlist (`cmd`, `internal`,
`go.mod`, `go.sum`). Adding a new top level directory that Go needs means
adding it to that fileset.

## State

Cache and watermarks live under `$XDG_STATE_HOME/ballpoint`, falling back to
`~/.local/state/ballpoint`. A relative `XDG_STATE_HOME` is ignored rather than
resolved against the working directory, which carries no meaning under a
timer.

## Consuming the flake

```nix
{
  inputs.ballpoint.url = "github:bashfulrobot/ballpoint";

  # in home-manager
  imports = [ inputs.ballpoint.homeManagerModules.default ];
  programs.ballpoint.enable = true;
}
```

`programs.ballpoint.package` defaults to this flake's build, so consumers do
not need to wire the overlay. `overlays.default` is exported for those who
want it.

### Prewarm timer

`ballpoint probe` needs no human input, so it should run on a schedule and be
warm before you start a triage session. Set `programs.ballpoint.prewarm.enable`
to run `ballpoint probe` on a systemd user timer.

```nix
programs.ballpoint = {
  enable = true;
  prewarm = {
    enable = true;
    onCalendar = "Mon..Fri 08,12,16:00";   # a few times across the working day
    concurrency = 8;                         # optional, 0 keeps the built-in default
    # secretsPath = "/home/you/.config/nixos-secrets/secrets.json";  # optional override
  };
};
```

The timer sets `OnCalendar`, `OnStartupSec`, `Persistent = true`, and
`RandomizedDelaySec`. A run missed while the machine was off is caught up on the
next boot, and a reboot triggers a fresh pass. The service is `Type = oneshot`
with `Restart = on-failure`, so a boot-time network race retries instead of
failing the day. The retry is bounded by `startLimitBurst` within
`startLimitIntervalSec`, so a permanent failure (a missing secrets file, a bad
token) stops looping and lets the unit fail instead of retrying forever. It is
not bound to `graphical-session.target`, so it runs whether or not a desktop
session is active. `secretsPath` is a path, not a credential, so nothing secret
enters the Nix store. The probe reads the values from that file at runtime.

Firing user timers at boot rather than at login needs systemd user lingering,
tracked in `nixerator#237`.

## Licence

MIT.
