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

  system = pkgs.stdenv.hostPlatform.system;

  # A bare self.packages.${system}.default throws a missing-attribute error
  # that names neither ballpoint nor the option to set instead.
  defaultPackage =
    self.packages.${system}.default or (throw ''
      ballpoint provides no package for ${system}.
      Set programs.ballpoint.package to a package you build yourself.
    '');
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
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];
  };
}
