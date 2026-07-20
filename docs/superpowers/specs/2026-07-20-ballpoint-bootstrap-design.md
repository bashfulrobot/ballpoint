# ballpoint bootstrap design

Design for issue #1: bootstrap the `github.com/bashfulrobot/ballpoint` Go
repository with flake outputs. This document fixes the layout decisions that
issues #2 through #6 build on.

## Problem

Todoist triage runs inside a skill today. Against a live 71 task scope each
`td` call costs 470 to 1050 ms, dominated by Node startup rather than network.
Card rendering is 44 ms, so nearly all per-task latency is CLI startup or a
model round trip spent displaying deterministic data.

Two of the three planned components cannot live in a skill. A freshness probe
runs headless on a systemd timer, and a timer invokes a binary. A keyboard
driven triage walk over a fixed keyword lexicon is a program, not a
conversation.

This repository is where those components live.

## Scope

In scope: module layout, flake outputs, CI, command skeleton, and the three
packages that carry real code today.

Out of scope: probe, TUI, and dispatcher logic. Consuming this flake from
nixerator.

## Architecture

### Command surface

`cmd/ballpoint/main.go` stays thin. It resolves the subcommand, delegates to
`internal/cli`, and translates a returned error into an exit code. Nothing
else.

Flag parsing uses the standard library. Three verbs (`probe`, `dispatch`, and
a bare invocation that will become the TUI) do not justify cobra's dependency
tree, and the issue specifies main as flag parsing only. Each subcommand owns
a `flag.FlagSet`, so adding flags in later issues is local to that subcommand.

`probe` and `dispatch` and the bare form each return a not-implemented error
and exit non-zero. Exiting zero would let a systemd timer in issue #4 record
success for work that never ran.

### Packages

Created now, because each carries code that is used and tested today:

- `internal/buildinfo` holds the version string. Stamped at link time and
  falling back to `runtime/debug.ReadBuildInfo` so `go run` reports something
  truthful rather than an empty string.
- `internal/config` resolves the state directory: `$XDG_STATE_HOME/ballpoint`,
  falling back to `~/.local/state/ballpoint` when the variable is unset or
  relative. Issues #2 and #3 store watermarks and cache here.
- `internal/cli` owns subcommand dispatch, usage text, and the not-implemented
  responses.

Deferred to the issues that fill them, each carrying only a `doc.go` package
comment naming that issue:

- `internal/sources` (#2), `internal/store` (#2), `internal/probe` (#3),
  `internal/tui` (#5), `internal/dispatch` (#6).

The alternative was scaffolding all six with invented interfaces. Rejected.
An interface designed before its first implementation encodes guesses, and
issue #2 would spend its budget unwinding them. A package comment naming the
owning issue communicates the layout without pretending the contract is
settled.

Everything sits under `internal/` because this is an application, not a
library.

### Dependency declaration

The issue asks that the Charm libraries be declared now so issue #5 does not
revisit dependency choices. Go removes modules that nothing imports, so a
plain `go.mod` entry would not survive `go mod tidy`.

`internal/tui/deps.go` carries blank imports behind a `tools` build tag.
`go mod tidy` matches all build configurations, so the imports hold the
modules in `go.mod` while contributing nothing to the built binary. This
covers bubbletea, lipgloss, bubbles, glamour, and huh, plus `errgroup` and
`golang.org/x/time/rate`, which issues #2 and #3 need for bounded concurrency
and per source rate limiting.

### Version stamping

`flake.nix` holds `version` as a single source of truth in a `let` binding,
matching upsight. It reaches the binary as
`-X github.com/bashfulrobot/ballpoint/internal/buildinfo.Version=${version}`.

### Flake outputs

Structure mirrors upsight: `flake-utils.lib.eachDefaultSystem`, an overlay
adding the package, and `nix/ballpoint.nix` consumed via `callPackage`.

- `packages.default` builds the binary with `buildGoModule`.
- `devShells.default` provides go, gopls, golangci-lint, and delve.
- `homeManagerModules.default` is a top level, system independent output,
  mirroring how hyprflake exposes `nixosModules.default`.

The module closes over the flake's own outputs so `programs.ballpoint.package`
defaults to this flake's build. A consumer enables the program without wiring
an overlay. Issue #4 adds `prewarm.enable` and `prewarm.interval` plus the
systemd user timer inside the `programs.ballpoint` namespace that already
exists, rather than restructuring the module.

### Error handling

Subcommands return `error`. `main` prints to stderr and exits non-zero.
`log/slog` provides structured output that lands cleanly in journald, which
matters once issue #4 runs the binary under a timer.

### Testing

Table driven tests with golden files under `testdata/`, establishing the
convention later issues follow.

- `internal/config`: state directory resolution across set, unset, empty, and
  relative `XDG_STATE_HOME`.
- `internal/cli`: argument dispatch, exit codes, and golden files for usage,
  version, and not-implemented output.
- `internal/buildinfo`: the unstamped fallback.

### CI

GitHub Actions on push and pull request, running the flake rather than a
parallel toolchain. Nix installs, then `nix build`, `nix develop --command go
test ./...`, and `nix develop --command golangci-lint run`.

Using `actions/setup-go` plus `golangci-lint-action` would be faster but would
pin the toolchain in a second place, so CI could pass while `nix develop`
fails. The flake is the single source of truth.

## Acceptance criteria mapping

| Criterion | Where |
| --- | --- |
| MIT licence and README | `LICENSE`, `README.md` |
| `go.mod` on current Go, module path | `go.mod` |
| Flake exposes three outputs | `flake.nix` |
| `nix build` produces a working binary | `nix/ballpoint.nix` |
| `nix develop` provides the four tools | `flake.nix` devShell |
| `--version` stamped via ldflags | `internal/buildinfo`, `nix/ballpoint.nix` |
| Subcommands return not implemented | `internal/cli` |
| CI runs build, test, lint | `.github/workflows/ci.yml` |
| `golangci-lint run` passes | `.golangci.yml` |

## Risks

`buildGoModule` needs a `vendorHash`. It is computed once here and changes
whenever `go.mod` changes, so issues #2 and #3 must update it. The README
records how.

The build tagged dependency file is load bearing but easy to delete by
accident, since nothing in the built binary references it. Its comment says
so.
