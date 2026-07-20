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
