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

  # An explicit allowlist rather than cleanSource. cleanSource strips .git but
  # keeps everything else, so a docs-only change rebuilt the whole Go tree,
  # and building from the main checkout swept in .claude/worktrees, which
  # holds full copies of the repo.
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../cmd
      ../internal
      ../go.mod
      ../go.sum
    ];
  };

  # Regenerate after any go.mod change: set this to lib.fakeHash, run
  # `nix build`, and copy the hash from the mismatch error.
  vendorHash = "sha256-U/542JIV4D6DplmfqDUwZ6kSfot4jaGiqSwiYyhRtwM=";

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
