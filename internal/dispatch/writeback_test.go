package dispatch

import (
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/queue"
)

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}

func TestWorklogArgvFull(t *testing.T) {
	a := Assessment{
		Summary: "PR merged",
		Verb:    "note",
		Links:   []Link{{Label: "PR 42", URL: "https://x/42"}},
		Next:    "close it",
	}
	got := WorklogArgv("/scripts", "id:42", a)
	eq(t, got, []string{
		"/scripts/td_worklog.sh", "id:42", "--entry", "PR merged",
		"--verb", "note", "--link", "PR 42=https://x/42", "--next", "close it",
	})
}

func TestWorklogArgvDefaultsVerbAndOmitsEmpty(t *testing.T) {
	got := WorklogArgv("/scripts", "id:7", Assessment{Summary: "just a note"})
	eq(t, got, []string{
		"/scripts/td_worklog.sh", "id:7", "--entry", "just a note", "--verb", "note",
	})
}

func TestDraftArgv(t *testing.T) {
	e := queue.Entry{Channel: "nudge", To: "#team", Body: "ping the owner"}
	got := DraftArgv("/scripts", "id:9", e)
	eq(t, got, []string{
		"/scripts/td_draft.sh", "id:9", "--channel", "nudge", "--to", "#team", "--text", "ping the owner",
	})
}

// A model that returns a link with a non-http scheme (javascript:, data:, file:)
// must not have that link land in the work log. WorklogArgv drops it.
func TestWorklogArgvDropsNonHTTPLinks(t *testing.T) {
	a := Assessment{
		Summary: "checked",
		Links: []Link{
			{Label: "safe", URL: "https://example.test/1"},
			{Label: "xss", URL: "javascript:alert(1)"},
			{Label: "exfil", URL: "data:text/html,<script>"},
			{Label: "local", URL: "file:///etc/passwd"},
		},
	}
	got := WorklogArgv("/s", "id:1", a)
	eq(t, got, []string{
		"/s/td_worklog.sh", "id:1", "--entry", "checked", "--verb", "note",
		"--link", "safe=https://example.test/1",
	})
}

// url.Parse accepts multibyte bidi and zero-width runes, so a scheme check alone
// would let a crafted URL ride into the work log and render deceptively in a
// Todoist client. A URL carrying such a rune is dropped.
func TestWorklogArgvDropsURLWithHiddenRunes(t *testing.T) {
	a := Assessment{
		Summary: "x",
		Links: []Link{
			{Label: "clean", URL: "https://h/ok"},
			{Label: "bidi", URL: "https://h/\u202egnp.exe"},
			{Label: "zerowidth", URL: "https://h/\u200bevil"},
		},
	}
	got := WorklogArgv("/s", "id:1", a)
	eq(t, got, []string{
		"/s/td_worklog.sh", "id:1", "--entry", "x", "--verb", "note",
		"--link", "clean=https://h/ok",
	})
}

// A label carrying "=" would make "label=url" ambiguous to the single-delimiter
// script contract, so the "=" is dropped before the join.
func TestWorklogArgvStripsEqualsFromLabel(t *testing.T) {
	a := Assessment{
		Summary: "x",
		Links:   []Link{{Label: "a=b=c", URL: "https://h/1"}},
	}
	got := WorklogArgv("/s", "id:1", a)
	eq(t, got, []string{
		"/s/td_worklog.sh", "id:1", "--entry", "x", "--verb", "note",
		"--link", "a b c=https://h/1",
	})
}

func TestSafeVerb(t *testing.T) {
	cases := map[string]string{
		"note":         "note",
		"":             "note",
		"--dry-run":    "dryrun",
		"Escalate":     "escalate",
		"  ":           "note",
		"a1b2":         "ab",
		"\x1b[31mnote": "mnote",
	}
	for in, want := range cases {
		if got := safeVerb(in); got != want {
			t.Errorf("safeVerb(%q) = %q, want %q", in, got, want)
		}
	}
}

// Control bytes and bidi overrides in a model-supplied summary or next step are
// stripped before they reach the script.
func TestWorklogArgvSanitizesSummaryAndNext(t *testing.T) {
	a := Assessment{
		Summary: "line1\x1b[2Jline2\u202ereversed",
		Next:    "do\x00 it",
	}
	got := WorklogArgv("/s", "id:1", a)
	eq(t, got, []string{
		"/s/td_worklog.sh", "id:1",
		"--entry", "line1[2Jline2reversed",
		"--verb", "note",
		"--next", "do it",
	})
}

// A tampered queue entry naming an unknown channel is not a valid draft.
func TestValidChannel(t *testing.T) {
	for _, ok := range []string{"nudge", "email", "teams"} {
		if !validChannel(ok) {
			t.Errorf("validChannel(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "slack", "NUDGE", "--channel", "webhook"} {
		if validChannel(bad) {
			t.Errorf("validChannel(%q) = true, want false", bad)
		}
	}
}

// DraftArgv strips control bytes and bidi from the recipient and body so a
// queued draft cannot smuggle escape sequences into the script or the dry-run
// terminal render.
func TestDraftArgvSanitizes(t *testing.T) {
	e := queue.Entry{Channel: "email", To: "a\x1b]0;evilb", Body: "hi\u202ethere\x07"}
	got := DraftArgv("/s", "id:1", e)
	eq(t, got, []string{
		"/s/td_draft.sh", "id:1", "--channel", "email", "--to", "a]0;evilb", "--text", "hithere",
	})
}
