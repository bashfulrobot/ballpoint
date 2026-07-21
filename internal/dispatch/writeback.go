package dispatch

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/sanitize"
)

// draftChannels is the closed set of channels the triage walk queues. The model
// never produces these (they come from the queue), but the queue is on-disk and
// could be tampered, so the channel is checked against this set before it
// crosses into td_draft.sh. See validChannel.
var draftChannels = map[string]bool{"nudge": true, "email": true, "teams": true}

// WorklogArgv builds the td_worklog.sh invocation that records an assessment.
// The entry, next step, and verb come from the model, so they are sanitized and
// the verb is coerced to a safe token (defaulting to "note"). Links become
// repeated --link "label=url" flags; a link is only included when its URL is a
// clean http or https URL (see safeURL), and any "=" in the label is dropped so
// the single-delimiter contract with the script is not ambiguous. A non-empty
// next step becomes --next.
func WorklogArgv(scriptsDir, ref string, a Assessment) []string {
	argv := []string{
		filepath.Join(scriptsDir, "td_worklog.sh"),
		ref,
		"--entry", sanitize.Block(a.Summary),
		"--verb", safeVerb(a.Verb),
	}
	for _, l := range a.Links {
		if !safeURL(l.URL) {
			continue
		}
		label := sanitize.Line(strings.ReplaceAll(l.Label, "=", " "))
		argv = append(argv, "--link", label+"="+l.URL)
	}
	if next := sanitize.Block(a.Next); next != "" {
		argv = append(argv, "--next", next)
	}
	return argv
}

// safeURL reports whether raw is an http or https URL that carries no rune the
// sanitizer would strip. url.Parse accepts multibyte bidi and zero-width runes,
// so a scheme check alone would let a crafted URL (for example one with a
// U+202E override or a zero-width character) ride into the work log and render
// deceptively in a Todoist client, which does not run this sanitizer. Requiring
// the URL to survive sanitize.Line unchanged closes that gap; a real URL never
// contains those runes, so nothing legitimate is dropped.
func safeURL(raw string) bool {
	return isHTTPURL(raw) && sanitize.Line(raw) == raw
}

// safeVerb coerces a model-supplied verb to a lowercase-letter token, defaulting
// to "note" when nothing usable remains. This keeps a crafted verb (leading
// dash, control bytes, whitespace) from reaching td_worklog.sh as anything but a
// plain word.
func safeVerb(v string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "note"
	}
	return b.String()
}

// isHTTPURL reports whether raw parses as an absolute http or https URL. A
// javascript:, data:, or file: URL from the model is rejected so it never lands
// in the work log as a live link.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// validChannel reports whether a queued draft names a known channel. An unknown
// channel is dropped rather than passed to td_draft.sh.
func validChannel(c string) bool { return draftChannels[c] }

// DraftArgv builds the td_draft.sh invocation that logs a prepared outward
// message as drafted. The script never sends; it records the draft in the work
// log with a draft verb. The recipient and body are sanitized so a queued draft
// cannot smuggle control bytes or bidi overrides into the script or into the
// dry-run render that prints this argv to the terminal. The caller has already
// checked the channel against validChannel.
func DraftArgv(scriptsDir, ref string, e queue.Entry) []string {
	return []string{
		filepath.Join(scriptsDir, "td_draft.sh"),
		ref,
		"--channel", e.Channel,
		"--to", sanitize.Line(e.To),
		"--text", sanitize.Block(e.Body),
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
