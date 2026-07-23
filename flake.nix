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

        checks = {
          # packages.default sets subPackages to cmd/ballpoint, which has no
          # tests, so its check phase reports "no test files" and stops.
          # Anyone running `nix flake check` locally would otherwise see green
          # having tested nothing.
          go-test = pkgs.ballpoint.overrideAttrs (old: {
            pname = "${old.pname}-tests";
            subPackages = null;
            doCheck = true;
          });

          # `nix flake check` does not evaluate homeManagerModules, so an eval
          # error there would ship green. Evaluating it here forces the option
          # surface, the lazy package default, and the prewarm timer/service
          # units, and asserts the unit settings the issue calls for.
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
                    programs.ballpoint.prewarm.secretsPath = "/tmp/x.json";
                    programs.ballpoint.prewarm.startLimitBurst = 3;
                    programs.ballpoint.prewarm.startLimitIntervalSec = "30min";
                  }
                ];
              };

              withDispatch = pkgs.lib.evalModules {
                specialArgs = { inherit pkgs; };
                modules = [
                  stubs
                  module
                  {
                    programs.ballpoint.enable = true;
                    programs.ballpoint.dispatch.enable = true;
                    programs.ballpoint.dispatch.onCalendar = "Mon 09:15";
                    programs.ballpoint.dispatch.concurrency = 4;
                    programs.ballpoint.dispatch.model = "haiku";
                  }
                ];
              };

              installed = pkgs.lib.head base.config.home.packages;
              service = withTimer.config.systemd.user.services.ballpoint-probe;
              timer = withTimer.config.systemd.user.timers.ballpoint-probe;
              dispatchService = withDispatch.config.systemd.user.services.ballpoint-dispatch;
              dispatchTimer = withDispatch.config.systemd.user.timers.ballpoint-dispatch;
              b2s = pkgs.lib.boolToString;

              # A newline in a str unit option would inject a second INI
              # directive, so the module must reject it at eval time.
              newlineRejected = builtins.tryEval (
                (pkgs.lib.evalModules {
                  specialArgs = { inherit pkgs; };
                  modules = [
                    stubs
                    module
                    {
                      programs.ballpoint.enable = true;
                      programs.ballpoint.prewarm.enable = true;
                      programs.ballpoint.prewarm.restartSec = "30s\nExecStartPre=/run/evil";
                    }
                  ];
                }).config.systemd.user.services.ballpoint-probe.Service.RestartSec
              );
              # systemd ExecStart quoting: both flags rendered, concurrency before
              # secrets-path, each argument double-quoted.
              wantExec = ''"probe" "--concurrency" "6" "--secrets-path" "/tmp/x.json"'';
              # Dispatch ExecStart: concurrency then model, each argument quoted.
              wantDispatchExec = ''"dispatch" "--concurrency" "4" "--model" "haiku"'';
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

              # ExecStart carries both flags in order, quoted the systemd way.
              actual=${pkgs.lib.escapeShellArg service.Service.ExecStart}
              expected=${pkgs.lib.escapeShellArg wantExec}
              case "$actual" in
                *"$expected") : ;;
                *) echo "unexpected ExecStart: $actual" >&2; exit 1 ;;
              esac

              # The on-failure restart is bounded, so a permanent failure stops looping.
              test "${toString service.Unit.StartLimitBurst}" = "3"
              test "${service.Unit.StartLimitIntervalSec}" = "30min"

              # A newline in a str unit option is rejected at eval time.
              test "${b2s newlineRejected.success}" = "false"

              # Timer: calendar plus boot, catches up missed runs, wanted by timers.target.
              test "${timer.Timer.OnCalendar}" = "Mon 09:00"
              test "${b2s timer.Timer.Persistent}" = "true"
              test -n "${timer.Timer.OnStartupSec}"
              test -n "${timer.Timer.RandomizedDelaySec}"
              test "${pkgs.lib.concatStringsSep "," timer.Install.WantedBy}" = "timers.target"

              # Dispatch disabled by default: enabling only the probe leaves no
              # dispatch timer, so the paid AI run is never scheduled unasked.
              test "${b2s (withTimer.config.systemd.user.timers ? ballpoint-dispatch)}" = "false"

              # Dispatch service: oneshot, retrying, ordered after the probe so it
              # assesses a fresh corpus, and carrying no Install section.
              test "${dispatchService.Service.Type}" = "oneshot"
              test "${dispatchService.Service.Restart}" = "on-failure"
              test "${pkgs.lib.concatStringsSep "," dispatchService.Unit.After}" = "ballpoint-probe.service"
              test "${b2s (dispatchService ? Install)}" = "false"

              # Dispatch ExecStart carries model and concurrency, quoted the systemd way.
              dactual=${pkgs.lib.escapeShellArg dispatchService.Service.ExecStart}
              dexpected=${pkgs.lib.escapeShellArg wantDispatchExec}
              case "$dactual" in
                *"$dexpected") : ;;
                *) echo "unexpected dispatch ExecStart: $dactual" >&2; exit 1 ;;
              esac

              # Dispatch timer: calendar schedule, wanted by timers.target.
              test "${dispatchTimer.Timer.OnCalendar}" = "Mon 09:15"
              test "${pkgs.lib.concatStringsSep "," dispatchTimer.Install.WantedBy}" = "timers.target"

              touch $out
            '';
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
