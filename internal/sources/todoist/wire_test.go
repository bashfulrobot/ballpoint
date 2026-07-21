package todoist

import (
	"testing"
	"time"
)

func TestRawTaskConvert(t *testing.T) {
	raw := rawTask{
		ID:        "10",
		Content:   "ship it",
		ProjectID: "p100",
		SectionID: "s5",
		Priority:  4,
		Labels:    []string{"work"},
		Desc:      "the description",
		URL:       "https://todoist.com/showTask?id=10",
		AddedAt:   "2026-07-18T12:00:00Z",
		UpdatedAt: "2026-07-20T09:00:00Z",
		Due:       &rawDue{Date: "2026-07-25", String: "Jul 25", IsRecurring: true},
	}

	projects := map[string]string{"p100": "Inbox"}
	sections := map[string]string{"s5": "Doing"}

	task := raw.toTask(projects, sections)

	if task.Priority != "p1" {
		t.Errorf("priority = %q, want p1", task.Priority)
	}
	if task.Project != "Inbox" {
		t.Errorf("project = %q, want Inbox", task.Project)
	}
	if task.Section != "Doing" {
		t.Errorf("section = %q, want Doing", task.Section)
	}
	if !task.Recurring {
		t.Error("recurring = false, want true")
	}
	if task.Due != "2026-07-25" {
		t.Errorf("due = %q, want 2026-07-25", task.Due)
	}
	want := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	if !task.UpdatedAt.Equal(want) {
		t.Errorf("updatedAt = %v, want %v", task.UpdatedAt, want)
	}
}

// A task never updated since creation falls back to added_at for its
// watermark time, so it is never zero.
func TestRawTaskWatermarkFallback(t *testing.T) {
	raw := rawTask{ID: "11", Content: "x", Priority: 1, AddedAt: "2026-07-18T12:00:00Z"}

	task := raw.toTask(nil, nil)

	want := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	if !task.UpdatedAt.Equal(want) {
		t.Errorf("updatedAt = %v, want added_at fallback %v", task.UpdatedAt, want)
	}
}

// An unknown project id resolves to the raw id rather than an empty string, so
// a card is never mislabelled as project-less.
func TestRawTaskUnknownProject(t *testing.T) {
	raw := rawTask{ID: "12", Content: "x", Priority: 1, ProjectID: "ghost", AddedAt: "2026-07-18T12:00:00Z"}

	task := raw.toTask(map[string]string{}, nil)

	if task.Project != "ghost" {
		t.Errorf("project = %q, want the raw id ghost", task.Project)
	}
}
