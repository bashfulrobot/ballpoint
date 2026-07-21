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
