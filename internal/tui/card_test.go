package tui

import (
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestBuildCardFreshnessLines(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	last := now.Add(-48 * time.Hour)
	task := sources.Task{ID: "1", Title: "fix repro", Project: "Kong", Section: "Engineering", Priority: "p2"}
	rep := probe.TaskReport{Links: []probe.LinkFreshness{
		{Key: "slack:c:1", System: "slack", Changed: true, LastActivity: &last},
		{Key: "aha:DEVP-I-42", System: "aha", Unchecked: true, Reason: probe.ReasonNoProbe},
	}}
	c := BuildCard(task, rep, now)
	if !c.Moved {
		t.Error("card with a Changed link should be Moved")
	}
	if len(c.Links) != 2 {
		t.Fatalf("want 2 link lines, got %d", len(c.Links))
	}
	if c.Links[0].State != LinkMoved {
		t.Errorf("slack link state = %v, want LinkMoved", c.Links[0].State)
	}
	if c.Links[1].State != LinkUnchecked || c.Links[1].Detail != string(probe.ReasonNoProbe) {
		t.Errorf("aha link = %+v, want unchecked with the reason", c.Links[1])
	}
}

func TestBuildCardFreshAge(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	last := now.Add(-3 * time.Hour)
	rep := probe.TaskReport{Links: []probe.LinkFreshness{
		{Key: "gmail:t", System: "gmail", LastActivity: &last},
	}}
	c := BuildCard(sources.Task{ID: "1"}, rep, now)
	if c.Moved {
		t.Error("a fresh, unchanged link should not mark the card moved")
	}
	if c.Links[0].State != LinkFresh || c.Links[0].Detail != "3h" {
		t.Errorf("fresh link = %+v, want fresh with age 3h", c.Links[0])
	}
}

func TestSortMovedFirstStable(t *testing.T) {
	cards := []Card{
		{TaskID: "a", Moved: false},
		{TaskID: "b", Moved: true},
		{TaskID: "c", Moved: false},
		{TaskID: "d", Moved: true},
	}
	SortMovedFirst(cards)
	got := []string{cards[0].TaskID, cards[1].TaskID, cards[2].TaskID, cards[3].TaskID}
	want := []string{"b", "d", "a", "c"} // moved first, original order within each group
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SortMovedFirst order = %v, want %v", got, want)
		}
	}
}

func TestHumanizeAgeNonNegative(t *testing.T) {
	if got := humanizeAge(-5 * time.Hour); got != "0m" {
		t.Errorf("humanizeAge(negative) = %q, want 0m", got)
	}
}
