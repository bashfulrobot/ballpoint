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
