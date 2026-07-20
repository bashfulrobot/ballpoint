# ballpoint

Cross-system task freshness probe, triage TUI, and work dispatcher.

Todoist triage as a program rather than a conversation: a freshness probe that
runs headless on a timer, a keyboard driven triage walk, and a dispatcher that
keeps long jobs from blocking the walk.

## Status

Bootstrap only. Every subcommand is wired and returns `not implemented`.

| Component | Issue |
| --- | --- |
| Source interface and Todoist HTTP client | #2 |
| Freshness probe engine | #3 |
| Prewarm timer as a home-manager module | #4 |
| Triage walk TUI | #5 |
| Dispatcher | #6 |

## Usage

```
ballpoint            walk the triage queue
ballpoint probe      refresh freshness data
ballpoint dispatch   run queued work
ballpoint --version  print the build version
```

Unimplemented subcommands exit non-zero, so the systemd timer in issue #4
cannot record success for work that never ran.

## Development

```
nix develop                                  # go, gopls, golangci-lint, delve
nix develop --command go test ./...
nix develop --command golangci-lint run
nix build && ./result/bin/ballpoint --version
```

Golden files under `testdata/` are regenerated with `go test ./... -update`.
Review the diff before committing.

The `-update` flag and the comparison helper live in `internal/golden`, not in
the package under test. `go test ./...` passes every flag to every test
binary, so a flag registered in one package makes `go test ./... -update` fail
in all the others. Test packages with no golden files of their own blank
import `internal/golden` to keep the flag universally defined.

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

## Licence

MIT.
