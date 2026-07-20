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
          # `nix flake check` does not evaluate homeManagerModules, so an eval
          # error there would ship green and land on issue #4, which is the
          # issue that has to extend the module. Evaluating it here forces
          # both the option surface and the lazy package default.
          hm-module =
            let
              evaluated = pkgs.lib.evalModules {
                specialArgs = { inherit pkgs; };
                modules = [
                  # Stands in for the home-manager option this module sets,
                  # so the check needs no home-manager input.
                  {
                    options.home.packages = pkgs.lib.mkOption {
                      type = pkgs.lib.types.listOf pkgs.lib.types.package;
                      default = [ ];
                    };
                  }
                  (import ./nix/hm-module.nix { inherit self; })
                  { programs.ballpoint.enable = true; }
                ];
              };

              installed = pkgs.lib.head evaluated.config.home.packages;
            in
            pkgs.runCommand "check-hm-module" { } ''
              test "${installed}" = "${self.packages.${system}.default}"
              test -x "${installed}/bin/ballpoint"
              touch $out
            '';
        };

        formatter = pkgs.nixpkgs-fmt;
      });
}
