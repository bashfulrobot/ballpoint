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
