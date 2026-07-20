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

### After changing dependencies

`nix/ballpoint.nix` pins a `vendorHash` covering the module graph, so any
`go.mod` change invalidates it. Set it to `lib.fakeHash`, run `nix build`, and
copy the hash from the mismatch error.

`internal/tui/deps.go` sits behind a `tools` build tag and holds the only
imports of the Charm libraries. It looks unused and is not: deleting it drops
those pins on the next `go mod tidy`.

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
