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
