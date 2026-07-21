# Prewarm Timer Home-Manager Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Export a home-manager module that schedules `ballpoint probe` on a systemd user timer (calendar + boot), and give the `probe` command the `--secrets-path` and `--concurrency` flags the module's option surface needs.

**Architecture:** `nix/hm-module.nix` grows a `programs.ballpoint.prewarm` namespace and emits a `Type=oneshot` `ballpoint-probe` user service plus a `ballpoint-probe` user timer (`OnCalendar`, `OnStartupSec`, `Persistent`, `RandomizedDelaySec`, wanted by `timers.target`, never `graphical-session.target`). The service passes the tunables through as CLI flags, so `ballpoint probe` gains `--secrets-path` (override the hardcoded `~/.config/nixos-secrets/secrets.json`) and `--concurrency` (thread into `todoist.WithConcurrency`). The flake's `hm-module` check evaluates the module with the timer enabled and asserts the produced unit settings, keeping the eval honest without a home-manager input.

**Tech Stack:** Go 1.26 (`flag`), Nix (home-manager module conventions, `lib.evalModules`), systemd user units.

---

## File Structure

- `internal/cli/cli.go` — extract `parseProbeFlags`; add `--secrets-path`, `--concurrency`. Modify.
- `internal/cli/probe.go` — `resolveProbeDeps` takes the parsed flags; thread secrets path and concurrency. Modify.
- `internal/cli/cli_test.go` — tests for `parseProbeFlags` and the path helper. Modify.
- `nix/hm-module.nix` — `programs.ballpoint.prewarm` options + systemd user service/timer. Modify.
- `flake.nix` — extend the `hm-module` check to assert the timer/service. Modify.
- `README.md` — document the module and the prewarm option surface. Modify.

---

## Task 1: Add `--secrets-path` and `--concurrency` to `ballpoint probe`

**Files:**
- Modify: `internal/cli/cli.go`, `internal/cli/probe.go`
- Test: `internal/cli/cli_test.go`

- [ ] **Step 1: Write the failing flag-parse tests**

Add to `internal/cli/cli_test.go`:

```go
func TestParseProbeFlags(t *testing.T) {
	var stderr bytes.Buffer
	f, helped, err := parseProbeFlags([]string{"--dry-run", "--secrets-path", "/tmp/s.json", "--concurrency", "4"}, &stderr)
	if err != nil {
		t.Fatalf("parseProbeFlags error = %v", err)
	}
	if helped {
		t.Fatal("parseProbeFlags reported help for a normal parse")
	}
	if !f.dryRun || f.secretsPath != "/tmp/s.json" || f.concurrency != 4 {
		t.Errorf("flags = %+v, want dryRun, /tmp/s.json, 4", f)
	}
}

// The defaults leave the secrets path empty (the binary default applies) and
// concurrency zero (the Todoist client default of 12 applies).
func TestParseProbeFlagsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	f, _, err := parseProbeFlags(nil, &stderr)
	if err != nil {
		t.Fatalf("parseProbeFlags error = %v", err)
	}
	if f.secretsPath != "" || f.concurrency != 0 {
		t.Errorf("defaults = %+v, want empty path and 0 concurrency", f)
	}
}

func TestParseProbeFlagsRejectsPositional(t *testing.T) {
	var stderr bytes.Buffer
	if _, _, err := parseProbeFlags([]string{"extra"}, &stderr); err == nil {
		t.Error("parseProbeFlags accepted a positional argument, want an error")
	}
}

// An empty --secrets-path resolves to the off-store default; a set path is used
// verbatim.
func TestSecretsPathOrDefault(t *testing.T) {
	if got, _ := secretsPathOrDefault("/x/y.json"); got != "/x/y.json" {
		t.Errorf("secretsPathOrDefault(set) = %q, want the given path", got)
	}
	got, err := secretsPathOrDefault("")
	if err != nil {
		t.Fatalf("secretsPathOrDefault(empty) error = %v", err)
	}
	if got == "" || !strings.HasSuffix(got, "nixos-secrets/secrets.json") {
		t.Errorf("secretsPathOrDefault(empty) = %q, want the off-store default", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nix develop --command go test ./internal/cli/ -run 'TestParseProbeFlags|TestSecretsPathOrDefault'`
Expected: FAIL — `parseProbeFlags` and `secretsPathOrDefault` undefined.

- [ ] **Step 3: Extract `parseProbeFlags` and add the flags in `cli.go`**

In `internal/cli/cli.go`, replace the entire `case "probe":` block with:

```go
	case "probe":
		f, helped, err := parseProbeFlags(rest[1:], stderr)
		if err != nil {
			return err
		}
		if helped {
			return nil
		}

		deps, err := resolveProbeDeps(f)
		if err != nil {
			return err
		}

		return runProbe(deps, stdout, stderr)
```

Add these above `Run` (after `displayName`):

```go
// probeFlags are the parsed flags of the probe subcommand.
type probeFlags struct {
	dryRun      bool
	benchmark   bool
	secretsPath string // empty means the off-store default
	concurrency int    // zero means the Todoist client default
}

// parseProbeFlags parses the probe subcommand's own FlagSet. helped is true when
// the caller asked for --help, which flag has already written, so Run returns
// nil without running the probe.
func parseProbeFlags(args []string, stderr io.Writer) (flags probeFlags, helped bool, err error) {
	pf := flag.NewFlagSet("probe", flag.ContinueOnError)
	pf.SetOutput(stderr)
	pf.BoolVar(&flags.dryRun, "dry-run", false, "report planned per-system calls without probing or writing watermarks")
	pf.BoolVar(&flags.benchmark, "benchmark", false, "time the real pass and print the wall clock")
	pf.StringVar(&flags.secretsPath, "secrets-path", "", "path to the off-store secrets file (default ~/.config/nixos-secrets/secrets.json)")
	pf.IntVar(&flags.concurrency, "concurrency", 0, "bounded Todoist fetch concurrency (default 12)")

	if err := pf.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return probeFlags{}, true, nil
		}
		return probeFlags{}, false, err
	}
	if pf.NArg() > 0 {
		return probeFlags{}, false, fmt.Errorf("probe takes no positional arguments, got %q", pf.Args())
	}
	return flags, false, nil
}
```

- [ ] **Step 4: Thread the flags through `resolveProbeDeps` in `probe.go`**

In `internal/cli/probe.go`, add `strings`-free helper and change the signature. Replace the `resolveProbeDeps` signature line and its path resolution:

Replace:
```go
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
```

With:
```go
func resolveProbeDeps(f probeFlags) (probeDeps, error) {
	dir, err := config.StateDir()
	if err != nil {
		return probeDeps{}, err
	}
	deps := probeDeps{stateDir: dir, dryRun: f.dryRun, benchmark: f.benchmark}

	path, err := secretsPathOrDefault(f.secretsPath)
	if err != nil {
		return probeDeps{}, err
	}
```

Change the Todoist client construction. Replace:
```go
	delta, err := todoist.New(token).Probe(context.Background(), sources.Watermark{})
```
With:
```go
	delta, err := todoist.New(token, todoist.WithConcurrency(f.concurrency)).Probe(context.Background(), sources.Watermark{})
```

Add the helper near `resolveProbeDeps`:
```go
// secretsPathOrDefault returns the explicit path when set, otherwise the
// off-store default. A flag override lets a systemd unit point at a per-host
// secrets file without an environment variable.
func secretsPathOrDefault(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return secrets.DefaultPath()
}
```

Note: `todoist.WithConcurrency` ignores values below 1, so passing 0 keeps the client default of 12.

- [ ] **Step 5: Run the CLI suite to verify it passes**

Run: `nix develop --command go test ./internal/cli/`
Expected: PASS. `TestRunRejectsStrayArguments` (`probe --nonexistent`) still errors, since the new FlagSet still rejects unknown flags.

- [ ] **Step 6: Update the usage string**

In `internal/cli/cli.go`, update the `usage` const probe line to:

```
  ballpoint probe [--dry-run] [--benchmark] [--secrets-path PATH] [--concurrency N]  refresh freshness data
```

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): add probe --secrets-path and --concurrency flags"
```

---

## Task 2: The prewarm option surface and systemd units

**Files:**
- Modify: `nix/hm-module.nix`

- [ ] **Step 1: Add the `prewarm` options and units**

In `nix/hm-module.nix`, add the option block inside `options.programs.ballpoint` (after `package`):

```nix
    prewarm = {
      enable = lib.mkEnableOption "the ballpoint probe prewarm timer";

      onCalendar = lib.mkOption {
        type = lib.types.str;
        default = "Mon..Fri 08,12,16:00";
        description = "systemd OnCalendar schedule for the prewarm run.";
      };

      onStartupSec = lib.mkOption {
        type = lib.types.str;
        default = "3min";
        description = ''
          Delay after boot before the first run, long enough that the network is
          usually up so the common case succeeds without waiting on a retry.
        '';
      };

      randomizedDelaySec = lib.mkOption {
        type = lib.types.str;
        default = "2min";
        description = "systemd RandomizedDelaySec, to spread scheduled runs.";
      };

      restartSec = lib.mkOption {
        type = lib.types.str;
        default = "30s";
        description = "Delay before an on-failure restart, so a boot-time network race retries.";
      };

      concurrency = lib.mkOption {
        type = lib.types.ints.unsigned;
        default = 0;
        description = "Bounded Todoist fetch concurrency. Zero uses the binary default (12).";
      };

      secretsPath = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "/home/alice/.config/nixos-secrets/secrets.json";
        description = ''
          Path to the off-store secrets file. Null uses the binary default,
          ~/.config/nixos-secrets/secrets.json. The value is a path, never a
          credential, so nothing secret enters the store.
        '';
      };
    };
```

Replace the `config` block:

```nix
  config = lib.mkIf cfg.enable (lib.mkMerge [
    { home.packages = [ cfg.package ]; }

    (lib.mkIf cfg.prewarm.enable {
      systemd.user.services.ballpoint-probe = {
        Unit.Description = "ballpoint freshness prewarm probe";
        Service = {
          Type = "oneshot";
          ExecStart = "${cfg.package}/bin/ballpoint ${probeArgs}";
          # A boot-time network race retries rather than failing the day. No
          # graphical-session binding, so it runs headless under the timer.
          Restart = "on-failure";
          RestartSec = cfg.prewarm.restartSec;
        };
      };

      systemd.user.timers.ballpoint-probe = {
        Unit.Description = "Schedule the ballpoint freshness prewarm probe";
        Timer = {
          OnCalendar = cfg.prewarm.onCalendar;
          OnStartupSec = cfg.prewarm.onStartupSec;
          Persistent = true;
          RandomizedDelaySec = cfg.prewarm.randomizedDelaySec;
        };
        Install.WantedBy = [ "timers.target" ];
      };
    })
  ]);
```

Add `probeArgs` to the `let` block (after `defaultPackage`):

```nix
  probeArgs = lib.escapeShellArgs (
    [ "probe" ]
    ++ lib.optionals (cfg.prewarm.concurrency > 0) [ "--concurrency" (toString cfg.prewarm.concurrency) ]
    ++ lib.optionals (cfg.prewarm.secretsPath != null) [ "--secrets-path" cfg.prewarm.secretsPath ]
  );
```

- [ ] **Step 2: Verify the module evaluates (checked in Task 3)**

Run: `nix flake check 2>&1 | tail -5`
Expected: FAIL for now, since the `hm-module` check does not yet stub `systemd.user.*`. Task 3 fixes the check. (If it already passes because the check does not touch the timer, that is fine; Task 3 adds the assertions.)

- [ ] **Step 3: Commit**

```bash
git add nix/hm-module.nix
git commit -m "feat(nix): add prewarm timer and service to the home-manager module"
```

---

## Task 3: Extend the flake `hm-module` check to assert the timer

**Files:**
- Modify: `flake.nix`

- [ ] **Step 1: Replace the `hm-module` check**

In `flake.nix`, replace the entire `hm-module = ...;` check attribute with:

```nix
          hm-module =
            let
              # Stub the home-manager options this module sets, so the check
              # needs no home-manager input. systemd.user.* is freeform here; the
              # assertions below read back the exact values the module produced.
              stubs = {
                options.home.packages = pkgs.lib.mkOption {
                  type = pkgs.lib.types.listOf pkgs.lib.types.package;
                  default = [ ];
                };
                options.systemd.user.services = pkgs.lib.mkOption {
                  type = pkgs.lib.types.attrsOf pkgs.lib.types.anything;
                  default = { };
                };
                options.systemd.user.timers = pkgs.lib.mkOption {
                  type = pkgs.lib.types.attrsOf pkgs.lib.types.anything;
                  default = { };
                };
              };

              module = import ./nix/hm-module.nix { inherit self; };

              base = pkgs.lib.evalModules {
                specialArgs = { inherit pkgs; };
                modules = [ stubs module { programs.ballpoint.enable = true; } ];
              };

              withTimer = pkgs.lib.evalModules {
                specialArgs = { inherit pkgs; };
                modules = [
                  stubs
                  module
                  {
                    programs.ballpoint.enable = true;
                    programs.ballpoint.prewarm.enable = true;
                    programs.ballpoint.prewarm.onCalendar = "Mon 09:00";
                    programs.ballpoint.prewarm.concurrency = 6;
                  }
                ];
              };

              installed = pkgs.lib.head base.config.home.packages;
              service = withTimer.config.systemd.user.services.ballpoint-probe;
              timer = withTimer.config.systemd.user.timers.ballpoint-probe;
              b2s = pkgs.lib.boolToString;
            in
            pkgs.runCommand "check-hm-module" { } ''
              test "${installed}" = "${self.packages.${system}.default}"
              test -x "${installed}/bin/ballpoint"

              # Prewarm disabled: no timer, so enabling ballpoint alone is inert.
              test "${b2s (base.config.systemd.user.timers ? ballpoint-probe)}" = "false"

              # Service: oneshot, retrying, and never bound to a graphical session
              # (it carries no Install section at all, so the timer alone starts it).
              test "${service.Service.Type}" = "oneshot"
              test "${service.Service.Restart}" = "on-failure"
              test "${b2s (service ? Install)}" = "false"
              case "${service.Service.ExecStart}" in
                *"/bin/ballpoint probe --concurrency 6") : ;;
                *) echo "unexpected ExecStart: ${service.Service.ExecStart}" >&2; exit 1 ;;
              esac

              # Timer: calendar plus boot, catches up missed runs, wanted by timers.target.
              test "${timer.Timer.OnCalendar}" = "Mon 09:00"
              test "${b2s timer.Timer.Persistent}" = "true"
              test -n "${timer.Timer.OnStartupSec}"
              test -n "${timer.Timer.RandomizedDelaySec}"
              test "${pkgs.lib.concatStringsSep "," timer.Install.WantedBy}" = "timers.target"

              touch $out
            '';
```

- [ ] **Step 2: Run the check to verify it passes**

Run: `nix flake check 2>&1 | tail -5`
Expected: `all checks passed!`

- [ ] **Step 3: Commit**

```bash
git add flake.nix
git commit -m "test(nix): assert the prewarm timer and service in the hm-module check"
```

---

## Task 4: Document the module and verify

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a prewarm section**

In `README.md`, after the `## Secrets` section, add:

```markdown
## Prewarm timer (home-manager)

`ballpoint probe` needs no human input, so it should run on a schedule and be
warm before a triage session starts. The flake exports
`homeManagerModules.default`, which installs the binary and, when
`programs.ballpoint.prewarm.enable` is set, schedules `ballpoint probe` on a
systemd user timer.

```nix
programs.ballpoint = {
  enable = true;
  prewarm = {
    enable = true;
    onCalendar = "Mon..Fri 08,12,16:00";   # a few times across the working day
    concurrency = 8;                         # optional; 0 keeps the default of 12
    # secretsPath = "/home/you/.config/nixos-secrets/secrets.json";  # optional override
  };
};
```

The timer sets `OnCalendar`, `OnStartupSec`, `Persistent = true`, and
`RandomizedDelaySec`, so a run missed while the machine was off is caught up on
next boot and reboots trigger a fresh pass. The service is `Type = oneshot` with
`Restart = on-failure`, so a boot-time network race retries rather than failing
the day, and it is not bound to `graphical-session.target`, so it runs whether or
not a desktop session is active. The secrets file path is a path, never a
credential, so nothing secret enters the Nix store; the probe reads the values
from that file at runtime.

Firing user timers at boot rather than at login needs systemd user lingering,
tracked in `nixerator#237`.
```

- [ ] **Step 2: Full verification**

Run:
```bash
nix develop --command go test ./...
nix develop --command golangci-lint run
nix flake check
```
Expected: all PASS, `all checks passed!`.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document the prewarm timer home-manager module"
```

---

## Self-Review

- **Spec coverage:** enable toggle, `onCalendar`, concurrency limit, secrets file path options (Task 2); `OnCalendar`/`OnStartupSec`/`Persistent`/`RandomizedDelaySec` on the timer (Task 2, asserted Task 3); `Type=oneshot` + `Restart=on-failure` + restart delay (Task 2, asserted Task 3); not bound to `graphical-session.target` (no Install on the service, asserted Task 3); credentials from a file path at runtime, never a session var or the store (`--secrets-path` is a path; Task 1 + README Task 4); timer visible in `list-timers` (Install.WantedBy timers.target, asserted Task 3); failed run leaves watermarks consistent and recovers (existing engine behavior of advancing watermarks only for checked links, plus `Restart=on-failure`; documented Task 4).
- **Placeholder scan:** none; every step carries concrete code.
- **Type consistency:** `probeFlags`, `parseProbeFlags`, `secretsPathOrDefault`, `resolveProbeDeps(f probeFlags)` line up across Task 1. The module namespace `programs.ballpoint.prewarm` and unit name `ballpoint-probe` match between Task 2 and the Task 3 assertions.
