package tui

import (
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// ScopeKind is how a scope selects tasks from the cached corpus.
type ScopeKind int

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
	var ids []string
	for _, t := range tasks {
		if s.matches(t) {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

func (s Scope) matches(t sources.Task) bool {
	switch s.Kind {
	case ScopeProject:
		return strings.EqualFold(t.Project, s.Value)
	case ScopeTask:
		return t.ID == s.Value
	case ScopeAll:
		return true
	case ScopeFilter, ScopePreset:
		// A full Todoist filter parser is out of scope. Degrade a raw filter to a
		// case-insensitive substring over the title, labels, and section, so the
		// cache can answer it offline. Documented limitation.
		q := strings.ToLower(s.Value)
		if strings.Contains(strings.ToLower(t.Title), q) {
			return true
		}
		for _, l := range t.Labels {
			if strings.Contains(strings.ToLower(l), q) {
				return true
			}
		}
		return strings.Contains(strings.ToLower(t.Section), q)
	}
	return false
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
