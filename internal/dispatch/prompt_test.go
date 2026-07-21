package dispatch

import (
	"strings"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func sampleTask() sources.Task {
	return sources.Task{
		ID:          "42",
		Title:       "Chase the migration",
		Project:     "Kong",
		Section:     "Doing",
		Priority:    "p2",
		Due:         "today",
		Description: "Blocked on infra.",
		Comments: []sources.Comment{
			{Content: "Pinged infra", PostedAt: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
		},
	}
}

func TestBuildPromptChangedGolden(t *testing.T) {
	last := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	report := probe.TaskReport{
		Title:      "Chase the migration",
		HasWorkLog: true,
		Links: []probe.LinkFreshness{
			{System: "github", Raw: "https://github.com/o/r/pull/42", Changed: true, LastActivity: &last},
			{System: "jira", Raw: "https://x/PROJ-1", Changed: false},
		},
	}
	got := BuildPrompt(sampleTask(), report, "NONCE")
	golden.Assert(t, "prompt_changed.golden", got)
}

func TestBuildPromptEmptyDeltaGolden(t *testing.T) {
	got := BuildPrompt(sampleTask(), probe.TaskReport{Title: "Chase the migration"}, "NONCE")
	golden.Assert(t, "prompt_empty.golden", got)
}

func TestBuildPromptBracketsUntrustedContent(t *testing.T) {
	got := BuildPrompt(sampleTask(), probe.TaskReport{}, "NONCE")
	if !strings.Contains(got, `<untrusted id="NONCE">`) || !strings.Contains(got, `</untrusted id="NONCE">`) {
		t.Errorf("prompt missing nonce brackets:\n%s", got)
	}
}

func TestBuildPromptSanitizesControlBytes(t *testing.T) {
	task := sampleTask()
	task.Title = "Chase \x1b[31mred\x07 migration"
	got := BuildPrompt(task, probe.TaskReport{}, "NONCE")
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Errorf("prompt kept control bytes:\n%q", got)
	}
}

// Bidi overrides and zero-width characters in task content are stripped, so a
// title cannot be visually reordered (Trojan Source) or padded with invisible
// runs to steer the model reading the prompt.
func TestBuildPromptSanitizesBidiAndZeroWidth(t *testing.T) {
	task := sampleTask()
	task.Title = "safe\u202etext\u200bhere"
	got := BuildPrompt(task, probe.TaskReport{}, "NONCE")
	for _, r := range []rune{0x202e, 0x200b} {
		if strings.ContainsRune(got, r) {
			t.Errorf("prompt kept dangerous rune %U:\n%q", r, got)
		}
	}
	if !strings.Contains(got, "safetexthere") {
		t.Errorf("prompt lost the visible title text:\n%q", got)
	}
}
