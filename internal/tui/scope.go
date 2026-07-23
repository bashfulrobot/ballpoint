package tui

import (
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// ScopeKind is how a scope selects tasks from the cached corpus.
type ScopeKind int

// ScopeKind values, one per way of selecting a subset of the corpus.
const (
	ScopeProject ScopeKind = iota
	ScopeFilter            // a raw or saved filter query
	ScopePreset            // a named preset, treated as a filter here
	ScopeTask              // a single task id
	ScopeAll               // everything cached (the fallback)
)

// Scope selects a subset of the cached corpus. It never fetches.
type Scope struct {
	Kind  ScopeKind
	Value string
}

// Resolve filters the cached corpus to the task ids in scope. An unknown value
// yields an empty result, which the TUI reports rather than hiding.
func (s Scope) Resolve(tasks []sources.Task) []string {
	match := s.predicate()
	var ids []string
	for _, t := range tasks {
		if match(t) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// predicate builds the per-task matcher once, so a filter expression is parsed a
// single time rather than per task.
func (s Scope) predicate() filterPredicate {
	switch s.Kind {
	case ScopeProject:
		return func(t sources.Task) bool { return strings.EqualFold(t.Project, s.Value) }
	case ScopeTask:
		return func(t sources.Task) bool { return t.ID == s.Value }
	case ScopeAll:
		return func(sources.Task) bool { return true }
	case ScopeFilter, ScopePreset:
		// Parse the Todoist filter subset the cache can answer offline. An
		// expression the parser does not accept (unsupported term, malformed
		// syntax) degrades to a case-insensitive substring over the title, labels,
		// and section, so nothing regresses and the walk is never dropped.
		if pred, ok := compileFilter(s.Value); ok {
			return pred
		}
		q := strings.ToLower(s.Value)
		return func(t sources.Task) bool { return substringMatch(t, q) }
	}
	return func(sources.Task) bool { return false }
}

// scopeFlagKeys maps a scope kind to its CLI flag name. The order matters only
// for deterministic selection when, in a misuse, more than one is set; the CLI
// layer rejects multiple scope flags before calling ParseScopeFlags.
var scopeFlagKeys = []struct {
	kind ScopeKind
	key  string
}{
	{ScopeProject, "project"},
	{ScopeFilter, "filter"},
	{ScopePreset, "preset"},
	{ScopeTask, "task"},
}

// ParseScopeFlags maps CLI flags to a Scope. ok is false when no scope flag was
// given, so the caller opens the interactive picker.
func ParseScopeFlags(flags map[string]string) (Scope, bool) {
	for _, f := range scopeFlagKeys {
		if v, ok := flags[f.key]; ok && v != "" {
			return Scope{Kind: f.kind, Value: v}, true
		}
	}
	return Scope{}, false
}
