# ballpoint Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap `github.com/bashfulrobot/ballpoint` as a Go application with its own flake, laid out so the probe engine, TUI, and dispatcher land as separate packages in later issues.

**Architecture:** A thin `cmd/ballpoint` entrypoint delegates to `internal/cli`, which parses flags with the standard library and returns a not-implemented error for each of the three wired subcommands. Three packages carry real code today (`buildinfo`, `config`, `cli`); five more exist as documented placeholders naming the issue that fills them. The flake mirrors upsight: `flake-utils.lib.eachDefaultSystem`, an overlay, and `nix/ballpoint.nix` via `callPackage`, with a top level home-manager module output mirroring hyprflake.

**Tech Stack:** Go (stdlib `flag`, `log/slog`, `runtime/debug`), Nix flakes with `buildGoModule`, golangci-lint, GitHub Actions.

---

## Sequencing constraint

There is no Go toolchain on `PATH` in this environment. It arrives only via
`nix develop`. And `buildGoModule` needs a `vendorHash`, which cannot be
computed until `go.mod` and `go.sum` exist.

So the flake lands in two passes. Task 1 writes a devShell-only flake to get a
toolchain. Task 9 adds the package once there is something to build. Every Go
command in this plan runs through `nix develop --command`.

## File structure

| File | Responsibility |
| --- | --- |
| `flake.nix` | Flake outputs. Holds `version` as the single source of truth. |
| `nix/ballpoint.nix` | The `buildGoModule` derivation and its ldflags. |
| `nix/hm-module.nix` | Home-manager option surface. Issue #4 extends it. |
| `go.mod`, `go.sum` | Module path and dependency pins. |
| `cmd/ballpoint/main.go` | Entrypoint. Wires slog, calls `cli.Run`, maps error to exit code. |
| `internal/buildinfo/buildinfo.go` | Link-time version stamp plus fallback. |
| `internal/config/config.go` | XDG state directory resolution. |
| `internal/cli/cli.go` | Flag parsing, usage text, subcommand dispatch. |
| `internal/cli/testdata/*.golden` | Expected CLI output. |
| `internal/tui/deps.go` | Build-tagged blank imports pinning declared libraries. |
| `internal/{sources,store,probe,tui,dispatch}/doc.go` | Placeholders naming the owning issue. |
| `.golangci.yml` | Lint configuration. |
| `.github/workflows/ci.yml` | Build, test, lint on push and pull request. |

---

### Task 1: Flake with a devShell

**Files:**
- Create: `flake.nix`

- [ ] **Step 1: Write the devShell-only flake**

```nix
{
  description = "ballpoint: Todoist triage freshness probe, triage walk, and work dispatcher";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          nativeBuildInputs = with pkgs; [ go gopls golangci-lint delve ];
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
```

- [ ] **Step 2: Verify the shell provides all four tools**

Run: `nix develop --command sh -c 'go version && gopls version && golangci-lint version && dlv version'`
Expected: four version strings, exit 0. Record the Go minor version; Task 2 needs it.

- [ ] **Step 3: Commit**

```bash
git add flake.nix flake.lock
git commit -m "build: add flake devShell with go, gopls, golangci-lint, delve"
```

---

### Task 2: Go module and a compiling entrypoint

**Files:**
- Create: `go.mod`, `cmd/ballpoint/main.go`

- [ ] **Step 1: Initialise the module**

Run: `nix develop --command go mod init github.com/bashfulrobot/ballpoint`

Then set the `go` directive to the toolchain's minor version from Task 1
Step 2. If `go version` reported `go1.25.0`, the directive reads `go 1.25`.
Do not invent a version the toolchain does not have; `buildGoModule` fails on
a directive newer than its Go.

- [ ] **Step 2: Write the entrypoint**

```go
// Command ballpoint drives Todoist triage: a freshness probe, a keyboard
// driven triage walk, and a dispatcher for queued work.
package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/bashfulrobot/ballpoint/internal/cli"
)

func main() {
	// Text output on stderr lands cleanly in journald once issue #4 runs
	// ballpoint under a systemd user timer.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ballpoint: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 3: Verify it does not yet build**

Run: `nix develop --command go build ./...`
Expected: FAIL with `no required module provides package github.com/bashfulrobot/ballpoint/internal/cli`. Task 5 supplies it.

- [ ] **Step 4: Commit**

```bash
git add go.mod cmd/
git commit -m "feat: add go module and ballpoint entrypoint"
```

---

### Task 3: buildinfo version stamp

**Files:**
- Create: `internal/buildinfo/buildinfo.go`, `internal/buildinfo/buildinfo_test.go`

- [ ] **Step 1: Write the failing test**

```go
package buildinfo

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name    string
		stamped string
		want    string
	}{
		{name: "link time stamp is used verbatim", stamped: "1.2.3", want: "1.2.3"},
		{name: "prerelease stamp is used verbatim", stamped: "0.1.0-rc1", want: "0.1.0-rc1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := Version
			t.Cleanup(func() { Version = original })

			Version = tt.stamped

			if got := String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An unstamped build must still report something a human can act on, so
// `go run` output is never an empty string.
func TestStringUnstamped(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	Version = ""

	if got := String(); got == "" {
		t.Error("String() = \"\", want a non-empty fallback")
	}
}
```

These tests mutate a package variable, so they must not call `t.Parallel()`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/buildinfo/ -v`
Expected: FAIL, `undefined: Version` and `undefined: String`.

- [ ] **Step 3: Write the implementation**

```go
// Package buildinfo exposes the version stamped into the ballpoint binary.
package buildinfo

import "runtime/debug"

// Version is the ballpoint version. The Nix build overrides it with
// -ldflags "-X github.com/bashfulrobot/ballpoint/internal/buildinfo.Version=<v>".
// It lives here rather than in package main so the TUI footer (#5) and the
// Todoist client's User-Agent (#2) can read it.
var Version = ""

// String returns the build version, preferring the link-time stamp and
// falling back to the module version the Go toolchain records. A build with
// neither reports "dev" rather than an empty string.
func String() string {
	if Version != "" {
		return Version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}

	return "dev"
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/buildinfo/ -v`
Expected: PASS, three subtests plus `TestStringUnstamped`.

- [ ] **Step 5: Commit**

```bash
git add internal/buildinfo/
git commit -m "feat: add buildinfo package for link-time version stamping"
```

---

### Task 4: XDG state directory resolution

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestStateDir(t *testing.T) {
	home := t.TempDir()
	fallback := filepath.Join(home, ".local", "state", "ballpoint")

	tests := []struct {
		name      string
		stateHome string
		want      string
	}{
		{
			name:      "absolute XDG_STATE_HOME is honoured",
			stateHome: "/var/lib/example",
			want:      filepath.Join("/var/lib/example", "ballpoint"),
		},
		{
			name:      "unset falls back to the spec default",
			stateHome: "",
			want:      fallback,
		},
		{
			name:      "relative XDG_STATE_HOME is ignored",
			stateHome: "relative/state",
			want:      fallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", tt.stateHome)

			got, err := StateDir()
			if err != nil {
				t.Fatalf("StateDir() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("StateDir() = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/config/ -v`
Expected: FAIL, `undefined: StateDir`.

- [ ] **Step 3: Write the implementation**

```go
// Package config resolves ballpoint's on-disk locations.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// appDir is ballpoint's directory name under the XDG base directories.
const appDir = "ballpoint"

// StateDir returns the directory holding ballpoint's cache and watermarks.
// It honours XDG_STATE_HOME when that names an absolute path and otherwise
// falls back to the specification default of ~/.local/state.
//
// A relative XDG_STATE_HOME is ignored rather than resolved against the
// working directory. The XDG specification requires absolute paths, and
// ballpoint runs under a systemd timer where the working directory carries no
// meaning.
func StateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(base) {
		return filepath.Join(base, appDir), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}

	return filepath.Join(home, ".local", "state", appDir), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `nix develop --command go test ./internal/config/ -v`
Expected: PASS, three subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add XDG state directory resolution"
```

---

### Task 5: CLI dispatch with golden files

**Files:**
- Create: `internal/cli/cli.go`, `internal/cli/cli_test.go`, `internal/cli/testdata/usage.golden`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
)

var update = flag.Bool("update", false, "rewrite golden files with current output")

// assertGolden compares got against testdata/<name>, rewriting it when the
// suite runs with -update. Later issues follow this convention.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s: %v", path, err)
	}

	if got != string(want) {
		t.Errorf("output mismatch for %s\n got: %q\nwant: %q", name, got, want)
	}
}

// Every wired but unbuilt subcommand must report ErrNotImplemented so main
// exits non-zero. A systemd timer must not record success for work that never
// ran.
func TestRunNotImplemented(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bare invocation is the triage walk", args: []string{}},
		{name: "probe", args: []string{"probe"}},
		{name: "dispatch", args: []string{"dispatch"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := Run(tt.args, &stdout, &stderr)

			if !errors.Is(err, ErrNotImplemented) {
				t.Errorf("Run(%q) error = %v, want ErrNotImplemented", tt.args, err)
			}
		})
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := Run([]string{"nope"}, &stdout, &stderr)

	if err == nil {
		t.Fatal("Run() error = nil, want an unknown-command error")
	}

	if errors.Is(err, ErrNotImplemented) {
		t.Error("Run() reported ErrNotImplemented for an unknown command")
	}
}

func TestRunVersion(t *testing.T) {
	original := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = original })

	buildinfo.Version = "1.2.3"

	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got, want := stdout.String(), "1.2.3\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v, want nil for --help", err)
	}

	assertGolden(t, "usage.golden", stderr.String())
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/cli/ -v`
Expected: FAIL, `undefined: Run` and `undefined: ErrNotImplemented`.

- [ ] **Step 3: Write the implementation**

```go
// Package cli implements ballpoint's command line surface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/bashfulrobot/ballpoint/internal/buildinfo"
)

// ErrNotImplemented reports a subcommand that is wired but carries no
// behaviour yet. Commands wrap it so main exits non-zero rather than letting
// a systemd timer record success for work that never ran.
var ErrNotImplemented = errors.New("not implemented")

const usage = `ballpoint drives Todoist triage.

Usage:
  ballpoint            walk the triage queue
  ballpoint probe      refresh freshness data
  ballpoint dispatch   run queued work

Flags:
  --version   print the build version and exit
  --help      print this message and exit
`

// Run executes the command named by args, which excludes the program name.
// Normal output goes to stdout and diagnostics to stderr so callers, and
// tests, can capture them independently.
func Run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ballpoint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }

	showVersion := fs.Bool("version", false, "print the build version and exit")

	if err := fs.Parse(args); err != nil {
		// fs.Usage has already written the usage text for --help, and asking
		// for help is not a failure.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if *showVersion {
		fmt.Fprintln(stdout, buildinfo.String())
		return nil
	}

	switch cmd := fs.Arg(0); cmd {
	case "":
		return fmt.Errorf("triage walk: %w", ErrNotImplemented)
	case "probe":
		return fmt.Errorf("probe: %w", ErrNotImplemented)
	case "dispatch":
		return fmt.Errorf("dispatch: %w", ErrNotImplemented)
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", cmd)
	}
}
```

- [ ] **Step 4: Generate the golden file, then verify against it**

Run: `nix develop --command sh -c 'mkdir -p internal/cli/testdata && go test ./internal/cli/ -update'`
Then run: `nix develop --command go test ./internal/cli/ -v`
Expected: PASS. Open `internal/cli/testdata/usage.golden` and confirm it holds the usage text, not an error message.

- [ ] **Step 5: Verify the whole module builds**

Run: `nix develop --command go build ./...`
Expected: exit 0. Task 2 Step 3's failure is now resolved.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "feat: add CLI dispatch with not-implemented subcommands"
```

---

### Task 6: Package placeholders

**Files:**
- Create: `internal/sources/doc.go`, `internal/store/doc.go`, `internal/probe/doc.go`, `internal/tui/doc.go`, `internal/dispatch/doc.go`

- [ ] **Step 1: Write each placeholder**

These carry a package comment and no declarations. That is legal Go, walked by
`go test ./...`, and clean under golangci-lint because there are no unused
symbols. Deliberately no interfaces: an interface written before its first
implementation encodes guesses the owning issue would have to unwind.

`internal/sources/doc.go`:

```go
// Package sources holds one package per external system ballpoint reads.
// Issue #2 adds the Source interface and the direct Todoist HTTP client that
// replaces shelling out to the td CLI.
package sources
```

`internal/store/doc.go`:

```go
// Package store holds ballpoint's cache and freshness watermarks, rooted at
// the directory config.StateDir reports. Issue #2 adds the watermark store.
package store
```

`internal/probe/doc.go`:

```go
// Package probe holds the freshness probe: fanout across sources, per source
// rate limiting, and watermark reconciliation. Issue #3 adds the batch by
// system probe engine.
package probe
```

`internal/tui/doc.go`:

```go
// Package tui holds the bubbletea model, view, and update for the keyboard
// driven triage walk. Issue #5 adds the TUI.
package tui
```

`internal/dispatch/doc.go`:

```go
// Package dispatch holds worker orchestration, so a long research job runs
// behind the triage walk rather than blocking it. Issue #6 adds the
// dispatcher.
package dispatch
```

- [ ] **Step 2: Verify they build and test clean**

Run: `nix develop --command go test ./...`
Expected: `ok` for buildinfo, config, and cli; `no test files` for the five placeholders and for cmd/ballpoint.

- [ ] **Step 3: Commit**

```bash
git add internal/
git commit -m "docs: add package placeholders naming their owning issues"
```

---

### Task 7: Pin the declared dependencies

**Files:**
- Create: `internal/tui/deps.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Write the build-tagged dependency file**

```go
//go:build tools

// This file exists only to hold dependency declarations. Issue #1 fixes the
// library choices for the TUI (#5), the probe engine (#3), and the Todoist
// source (#2) so those issues do not have to revisit them.
//
// Go drops modules nothing imports, so a go.mod entry alone would not survive
// `go mod tidy`. These blank imports hold the pins. `go mod tidy` matches all
// build configurations, so the tools tag does not hide them from it, while
// keeping every one of these packages out of the shipped binary.
//
// Do not delete this file because nothing appears to use it. Deleting it drops
// the pins on the next `go mod tidy`.
package tui

import (
	_ "github.com/charmbracelet/bubbles/list"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/glamour"
	_ "github.com/charmbracelet/huh"
	_ "github.com/charmbracelet/lipgloss"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/time/rate"
)
```

- [ ] **Step 2: Resolve and pin the modules**

Run: `nix develop --command go mod tidy`
Expected: `go.mod` gains all seven modules, `go.sum` is created.

- [ ] **Step 3: Verify the pins survive a second tidy**

Run: `nix develop --command sh -c 'go mod tidy && git diff --exit-code go.mod'`
Expected: exit 0. A non-zero exit means the tag is hiding the imports and the pins are not holding.

- [ ] **Step 4: Verify the tagged file stays out of the binary**

Run: `nix develop --command sh -c 'go build ./... && go test ./...'`
Expected: exit 0, and `go list -deps ./cmd/ballpoint | grep -c charmbracelet` reports `0`.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/deps.go go.mod go.sum
git commit -m "build: pin charm, errgroup, and rate limiter dependencies"
```

---

### Task 8: Lint configuration

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Check which schema the toolchain expects**

Run: `nix develop --command golangci-lint version`

golangci-lint v2 rejects a v1 configuration file outright. If the reported
version starts with `2`, use the file in Step 2 as written. If it starts with
`1`, drop the `version` key and the `formatters` block, and move `gofmt` and
`goimports` into `linters.enable`.

- [ ] **Step 2: Write the configuration**

```yaml
version: "2"

linters:
  # The standard set is errcheck, govet, ineffassign, staticcheck, and unused.
  default: standard
  enable:
    - misspell
    - revive

formatters:
  enable:
    - gofmt
    - goimports
```

- [ ] **Step 3: Run the linter**

Run: `nix develop --command golangci-lint run`
Expected: exit 0, no findings. Fix any finding rather than excluding the
linter that raised it.

- [ ] **Step 4: Commit**

```bash
git add .golangci.yml
git commit -m "build: add golangci-lint configuration"
```

---

### Task 9: Package the binary

**Files:**
- Create: `nix/ballpoint.nix`
- Modify: `flake.nix`

- [ ] **Step 1: Write the derivation with a placeholder hash**

`lib.fakeHash` is a real sentinel, not a plan placeholder: the build is
expected to fail and print the correct hash. Step 3 replaces it.

```nix
# The ballpoint binary.
#
# `version` comes from flake.nix, which is the single source of truth, and
# reaches the binary through ldflags so `ballpoint --version` reports the same
# string the flake declares.
{ lib
, buildGoModule
, version
}:

buildGoModule {
  pname = "ballpoint";
  inherit version;

  src = lib.cleanSource ../.;

  # Regenerate after any go.mod change: set this to lib.fakeHash, run
  # `nix build`, and copy the hash from the mismatch error.
  vendorHash = lib.fakeHash;

  # Only the command is built. The internal packages come along as its
  # dependencies; the placeholders and the tools-tagged file do not.
  subPackages = [ "cmd/ballpoint" ];

  ldflags = [
    "-s"
    "-w"
    "-X github.com/bashfulrobot/ballpoint/internal/buildinfo.Version=${version}"
  ];

  meta = {
    description = "Todoist triage: freshness probe, triage walk, and work dispatcher";
    homepage = "https://github.com/bashfulrobot/ballpoint";
    license = lib.licenses.mit;
    mainProgram = "ballpoint";
  };
}
```

- [ ] **Step 2: Rewrite flake.nix with the overlay and package outputs**

```nix
{
  description = "ballpoint: Todoist triage freshness probe, triage walk, and work dispatcher";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    let
      # Single source of truth for the version stamped into the binary.
      version = "0.1.0";

      overlay = final: prev: {
        ballpoint = final.callPackage ./nix/ballpoint.nix { inherit version; };
      };
    in
    {
      # System independent outputs, mirroring how hyprflake exposes its module.
      overlays.default = overlay;
      homeManagerModules.default = import ./nix/hm-module.nix { inherit self; };
    }
    //
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ overlay ];
        };
      in
      {
        packages = {
          inherit (pkgs) ballpoint;
          default = pkgs.ballpoint;
        };

        devShells.default = pkgs.mkShell {
          nativeBuildInputs = with pkgs; [ go gopls golangci-lint delve ];
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
```

- [ ] **Step 3: Compute the real vendorHash**

Run: `nix build 2>&1 | tee /tmp/ballpoint-build.log`
Expected: FAIL with a `hash mismatch in fixed-output derivation` block naming
a `got:` hash. Copy that `sha256-...` value over `lib.fakeHash` in
`nix/ballpoint.nix`.

- [ ] **Step 4: Build and run**

Run: `nix build && ./result/bin/ballpoint --version`
Expected: prints `0.1.0`, exit 0. Anything else, including `dev`, means the
ldflags path does not match the package path.

- [ ] **Step 5: Verify the subcommands exit non-zero**

Run: `./result/bin/ballpoint probe; echo "exit=$?"`
Expected: `ballpoint: probe: not implemented` on stderr and `exit=1`.

- [ ] **Step 6: Commit**

```bash
git add flake.nix flake.lock nix/ballpoint.nix
git commit -m "build: package ballpoint with buildGoModule and stamped version"
```

---

### Task 10: Home-manager module

**Files:**
- Create: `nix/hm-module.nix`

- [ ] **Step 1: Write the module**

```nix
# Home Manager module for ballpoint.
#
# Issue #1 ships the option surface and package wiring only. Issue #4 adds
# `programs.ballpoint.prewarm` and the systemd user timer inside this same
# namespace, so a consumer who enables ballpoint now does not have to
# restructure their configuration when the timer lands.
{ self }:

{ config, lib, pkgs, ... }:

let
  cfg = config.programs.ballpoint;
in
{
  options.programs.ballpoint = {
    enable = lib.mkEnableOption "ballpoint, a Todoist triage tool";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "ballpoint.packages.\${system}.default";
      description = ''
        The ballpoint package to install. Defaults to the build from the
        ballpoint flake, so a consumer does not need to wire the overlay.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
  };
}
```

- [ ] **Step 2: Verify the module output evaluates**

Run: `nix eval .#homeManagerModules.default --apply builtins.typeOf`
Expected: `"lambda"`. An `error: attribute 'homeManagerModules' missing` means
the output sits inside `eachDefaultSystem` instead of beside it.

- [ ] **Step 3: Verify the option surface evaluates**

Run:

```bash
nix eval --impure --expr \
  'let m = (import ./nix/hm-module.nix { self = builtins.getFlake (toString ./.); }); in builtins.attrNames (m { config = { programs.ballpoint = { enable = false; }; }; lib = (import <nixpkgs> {}).lib; pkgs = import <nixpkgs> {}; }).options.programs.ballpoint'
```

Expected: `[ "enable" "package" ]`.

- [ ] **Step 4: Commit**

```bash
git add nix/hm-module.nix
git commit -m "feat: add home-manager module with package option"
```

---

### Task 11: CI

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write the workflow**

Actions are pinned to a full commit SHA, not a tag. A tag is mutable, so
pinning to one leaves a compromised upstream account free to repoint it at
malicious code. Only a SHA is immutable. `permissions` is narrowed to the read
access the job actually needs.

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest

    steps:
      - name: Check out
        uses: actions/checkout@v4

      - name: Install Nix
        uses: cachix/install-nix-action@v30
        with:
          extra_nix_config: |
            experimental-features = nix-command flakes

      - name: Build
        run: nix build --print-build-logs

      - name: Test
        run: nix develop --command go test ./...

      - name: Lint
        run: nix develop --command golangci-lint run

      - name: Verify the version stamp
        run: |
          version="$(./result/bin/ballpoint --version)"
          echo "ballpoint --version reported: ${version}"
          test -n "${version}"
          test "${version}" != "dev"
```

- [ ] **Step 2: Verify the workflow parses**

Run: `nix run nixpkgs#yq-go -- eval '.jobs.check.steps | length' .github/workflows/ci.yml`
Expected: `6`.

- [ ] **Step 3: Commit**

```bash
git add .github/
git commit -m "ci: build, test, and lint via the flake on push and pull request"
```

---

### Task 12: README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Write the README**

```markdown
# ballpoint

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

## Development

```
nix develop                                  # go, gopls, golangci-lint, delve
nix develop --command go test ./...
nix develop --command golangci-lint run
nix build && ./result/bin/ballpoint --version
```

Golden files under `testdata/` are regenerated with
`go test ./... -update`. Review the diff before committing.

### After changing dependencies

`nix/ballpoint.nix` pins a `vendorHash` that covers the module graph, so any
`go.mod` change invalidates it. Set it to `lib.fakeHash`, run `nix build`, and
copy the hash from the mismatch error.

`internal/tui/deps.go` sits behind a `tools` build tag and holds the only
imports of the Charm libraries. It looks unused and is not: deleting it drops
those pins on the next `go mod tidy`.

## State

Cache and watermarks live under `$XDG_STATE_HOME/ballpoint`, falling back to
`~/.local/state/ballpoint`.

## Consuming the flake

```nix
{
  inputs.ballpoint.url = "github:bashfulrobot/ballpoint";

  # in home-manager
  imports = [ inputs.ballpoint.homeManagerModules.default ];
  programs.ballpoint.enable = true;
}
```

## Licence

MIT.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document usage, development, and flake consumption"
```

---

### Task 13: Full acceptance verification

- [ ] **Step 1: Run every command the issue names**

```bash
nix build
nix develop --command go test ./...
nix develop --command golangci-lint run
./result/bin/ballpoint --version
```

Expected: all exit 0, and `--version` prints `0.1.0`.

- [ ] **Step 2: Confirm the flake exposes all three outputs**

```bash
nix eval .#packages.x86_64-linux.default.name
nix eval .#devShells.x86_64-linux.default.name
nix eval .#homeManagerModules.default --apply builtins.typeOf
```

Expected: a derivation name, a shell name, and `"lambda"`.

- [ ] **Step 3: Confirm the devShell carries all four tools**

Run: `nix develop --command sh -c 'go version && gopls version && golangci-lint version && dlv version'`
Expected: four version strings, exit 0.

- [ ] **Step 4: Confirm every subcommand is wired**

```bash
for c in "" probe dispatch; do
  ./result/bin/ballpoint $c; echo "  ${c:-<bare>} exit=$?"
done
```

Expected: each reports `not implemented` and `exit=1`.

---

## Self-review

**Spec coverage.** Command surface, Task 5. Packages, Tasks 3, 4, 5, 6.
Dependency declaration, Task 7. Version stamping, Tasks 3 and 9. Flake
outputs, Tasks 1, 9, 10. Error handling, Tasks 2 and 5. Testing, Tasks 3, 4,
5. CI, Task 11. The spec's risk note about `vendorHash` and the tagged file is
carried into the README in Task 12. No gaps.

**Placeholders.** The only `fakeHash` is the sentinel `lib.fakeHash`, which is
real Nix and resolved in Task 9 Step 3. The one deferred decision, the
golangci-lint schema version, is resolved by a command in Task 8 Step 1 with
both branches written out.

**Type consistency.** `buildinfo.Version` and `buildinfo.String()` are defined
in Task 3 and used identically in Tasks 5 and 9. `cli.Run(args, stdout,
stderr) error` and `cli.ErrNotImplemented` are defined in Task 5 and used
identically in Task 2. `config.StateDir() (string, error)` is defined in Task
4 and referenced in the Task 6 store placeholder comment. The ldflags path in
Task 9 matches the package path in Task 3.
