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
