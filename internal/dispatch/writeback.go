package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/bashfulrobot/ballpoint/internal/queue"
)

// WorklogArgv builds the td_worklog.sh invocation that records an assessment.
// Verb defaults to "note". Links become repeated --link "label=url" flags, and
// a non-empty next step becomes --next.
func WorklogArgv(scriptsDir, ref string, a Assessment) []string {
	verb := a.Verb
	if verb == "" {
		verb = "note"
	}
	argv := []string{
		filepath.Join(scriptsDir, "td_worklog.sh"),
		ref,
		"--entry", a.Summary,
		"--verb", verb,
	}
	for _, l := range a.Links {
		argv = append(argv, "--link", l.Label+"="+l.URL)
	}
	if a.Next != "" {
		argv = append(argv, "--next", a.Next)
	}
	return argv
}

// DraftArgv builds the td_draft.sh invocation that logs a prepared outward
// message as drafted. The script never sends; it records the draft in the work
// log with a draft verb.
func DraftArgv(scriptsDir, ref string, e queue.Entry) []string {
	return []string{
		filepath.Join(scriptsDir, "td_draft.sh"),
		ref,
		"--channel", e.Channel,
		"--to", e.To,
		"--text", e.Body,
	}
}

// ExecScript runs one script argv, capturing combined output for diagnostics.
// It is the real RunScript passed into Config; tests inject a fake instead.
func ExecScript(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("empty script argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("running %s: %w: %s", argv[0], err, out)
	}
	return nil
}
