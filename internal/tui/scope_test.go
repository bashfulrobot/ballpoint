package tui

import (
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestScopeProject(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Project: "Kong"}, {ID: "2", Project: "Home"}, {ID: "3", Project: "Kong"},
	}
	ids := Scope{Kind: ScopeProject, Value: "kong"}.Resolve(tasks) // case-insensitive
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "3" {
		t.Fatalf("project scope = %v, want [1 3]", ids)
	}
}

func TestScopeSingleTask(t *testing.T) {
	tasks := []sources.Task{{ID: "1"}, {ID: "2"}}
	ids := Scope{Kind: ScopeTask, Value: "2"}.Resolve(tasks)
	if len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("task scope = %v, want [2]", ids)
	}
}

func TestScopeFilterSubstring(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Title: "chase the repro"},
		{ID: "2", Title: "unrelated", Labels: []string{"repro-needed"}},
		{ID: "3", Title: "nope"},
	}
	ids := Scope{Kind: ScopeFilter, Value: "repro"}.Resolve(tasks)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("filter scope = %v, want [1 2]", ids)
	}
}

func TestScopeFilterGrammar(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Project: "Work", Labels: []string{"waiting"}},
		{ID: "2", Project: "Work", Labels: []string{"someday"}}, // right project, wrong label
		{ID: "3", Project: "Home", Labels: []string{"waiting"}}, // right label, wrong project
		{ID: "4", Project: "Work", Labels: []string{"waiting"}, Priority: "p1"},
	}
	// @waiting & #Work matches only the waiting-labelled tasks in Work.
	ids := Scope{Kind: ScopeFilter, Value: "@waiting & #Work"}.Resolve(tasks)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "4" {
		t.Fatalf("@waiting & #Work = %v, want [1 4]", ids)
	}
}

func TestScopeFilterPriorityOrUnsupported(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Priority: "p1", Title: "urgent"},
		{ID: "2", Priority: "p4", Title: "look at overdue backlog"}, // substring hit on "overdue"
		{ID: "3", Priority: "p3", Title: "nothing here"},
	}
	// p1 | overdue: p1 matches the priority; "overdue" is unsupported and degrades
	// to a substring, so the walk still parses and returns both.
	ids := Scope{Kind: ScopeFilter, Value: "p1 | overdue"}.Resolve(tasks)
	if len(ids) != 2 || ids[0] != "1" || ids[1] != "2" {
		t.Fatalf("p1 | overdue = %v, want [1 2]", ids)
	}
}

func TestScopeFilterMalformedDegradesToSubstring(t *testing.T) {
	tasks := []sources.Task{
		{ID: "1", Title: "note about (@waiting syntax"}, // literal substring of the raw value
		{ID: "2", Title: "nope"},
	}
	// Unbalanced parens: the parser rejects it, so the whole raw value is used as
	// a substring rather than panicking or dropping the walk.
	ids := Scope{Kind: ScopeFilter, Value: "(@waiting"}.Resolve(tasks)
	if len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("malformed filter = %v, want [1] via substring fallback", ids)
	}
}

func TestScopeAll(t *testing.T) {
	tasks := []sources.Task{{ID: "1"}, {ID: "2"}}
	if ids := (Scope{Kind: ScopeAll}).Resolve(tasks); len(ids) != 2 {
		t.Fatalf("all scope = %v, want both", ids)
	}
}

func TestParseScopeFlags(t *testing.T) {
	s, ok := ParseScopeFlags(map[string]string{"project": "Kong"})
	if !ok || s.Kind != ScopeProject || s.Value != "Kong" {
		t.Fatalf("ParseScopeFlags = %+v ok=%v", s, ok)
	}
	if _, ok := ParseScopeFlags(map[string]string{}); ok {
		t.Error("ParseScopeFlags with no flags should report ok=false so the picker opens")
	}
	if _, ok := ParseScopeFlags(map[string]string{"project": ""}); ok {
		t.Error("an empty flag value should not count as a scope")
	}
}
