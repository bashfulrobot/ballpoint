# Home Manager module for ballpoint.
#
# Installs the binary, and under `programs.ballpoint.prewarm` schedules
# `ballpoint probe` on a systemd user timer so freshness data is warm before a
# triage session starts.
{ self }:

{ config, lib, pkgs, ... }:

let
  cfg = config.programs.ballpoint;

  system = pkgs.stdenv.hostPlatform.system;

  # A bare self.packages.${system}.default throws a missing-attribute error
  # that names neither ballpoint nor the option to set instead.
  defaultPackage =
    self.packages.${system}.default or (throw ''
      ballpoint provides no package for ${system}.
      Set programs.ballpoint.package to a package you build yourself.
    '');

  # systemd parses ExecStart with its own quoting rules, not a shell's, so shell
  # escaping (lib.escapeShellArgs) can emit sequences systemd rejects: it escapes
  # an embedded single quote as '\'' , putting a backslash right after a closing
  # quote, which systemd.syntax(7) forbids. That would break a secretsPath
  # containing an apostrophe. This mirrors nixpkgs' own escapeSystemdExecArgs
  # (a NixOS-internal util not exposed in lib): JSON-quote each argument, then
  # escape systemd's % and $ specifiers.
  escapeSystemdExecArg = arg: lib.replaceStrings [ "%" "$" ] [ "%%" "$$" ] (builtins.toJSON arg);
  escapeSystemdExecArgs = args: lib.concatMapStringsSep " " escapeSystemdExecArg args;

  # Unit files are INI, so a newline in a str option value would render as a
  # second line and inject another directive. Reject it at eval time, the same
  # defensive posture applied to ExecStart, since an option value can come from
  # an imported or generated module, not only the machine owner.
  assertNoNewline = name: value:
    if lib.hasInfix "\n" value || lib.hasInfix "\r" value
    then throw "programs.ballpoint.prewarm.${name} must not contain a newline"
    else value;

  # The probe invocation the timer runs. Concurrency and secrets path are passed
  # only when set, so the binary's own defaults apply otherwise. The secrets path
  # is a path, never a credential, so nothing secret enters the store.
  probeCommand = escapeSystemdExecArgs (
    [ "${cfg.package}/bin/ballpoint" "probe" ]
    ++ lib.optionals (cfg.prewarm.concurrency > 0) [ "--concurrency" (toString cfg.prewarm.concurrency) ]
    ++ lib.optionals (cfg.prewarm.secretsPath != null) [ "--secrets-path" cfg.prewarm.secretsPath ]
  );
in
{
  options.programs.ballpoint = {
    enable = lib.mkEnableOption "ballpoint, a Todoist triage tool";

    package = lib.mkOption {
      type = lib.types.package;
      default = defaultPackage;
      defaultText = lib.literalExpression "ballpoint.packages.\${system}.default";
      description = ''
        The ballpoint package to install. Defaults to the build from the
        ballpoint flake, so a consumer does not need to wire the overlay.
      '';
    };

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

      startLimitIntervalSec = lib.mkOption {
        type = lib.types.str;
        default = "1h";
        description = ''
          Window over which startLimitBurst restarts are counted. Without a
          bound, an on-failure restart every restartSec turns a permanent
          failure (a missing secrets file, a bad token) into a loop that never
          gives up. The timer still re-fires on onCalendar and onStartupSec, so
          the restart only has to cover a transient boot race. Setting this to
          "0" disables the bound, so the service would retry forever.
        '';
      };

      startLimitBurst = lib.mkOption {
        type = lib.types.ints.unsigned;
        default = 5;
        description = ''
          Restarts allowed within startLimitIntervalSec before systemd stops
          retrying and lets the unit fail. Zero disables the bound.
        '';
      };

      concurrency = lib.mkOption {
        type = lib.types.ints.unsigned;
        default = 0;
        description = "Bounded Todoist fetch concurrency. Zero uses the binary's built-in default.";
      };

      secretsPath = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "/home/alice/.config/nixos-secrets/secrets.json";
        description = ''
          Path to the off-store secrets file. Null uses the binary's built-in
          default path. The value is a path, never a credential, so nothing
          secret enters the store.
        '';
      };
    };
  };

  config = lib.mkIf cfg.enable (lib.mkMerge [
    { home.packages = [ cfg.package ]; }

    (lib.mkIf cfg.prewarm.enable {
      systemd.user.services.ballpoint-probe = {
        Unit = {
          Description = "ballpoint freshness prewarm probe";
          # Bound the on-failure restart so a permanent failure stops looping
          # instead of retrying every restartSec forever.
          StartLimitIntervalSec = assertNoNewline "startLimitIntervalSec" cfg.prewarm.startLimitIntervalSec;
          StartLimitBurst = cfg.prewarm.startLimitBurst;
        };
        Service = {
          Type = "oneshot";
          ExecStart = probeCommand;
          # A boot-time network race retries rather than failing the day. No
          # graphical-session binding, so it runs headless under the timer.
          Restart = "on-failure";
          RestartSec = assertNoNewline "restartSec" cfg.prewarm.restartSec;
        };
      };

      systemd.user.timers.ballpoint-probe = {
        Unit.Description = "Schedule the ballpoint freshness prewarm probe";
        Timer = {
          OnCalendar = assertNoNewline "onCalendar" cfg.prewarm.onCalendar;
          OnStartupSec = assertNoNewline "onStartupSec" cfg.prewarm.onStartupSec;
          Persistent = true;
          RandomizedDelaySec = assertNoNewline "randomizedDelaySec" cfg.prewarm.randomizedDelaySec;
        };
        Install.WantedBy = [ "timers.target" ];
      };
    })
  ]);
}
