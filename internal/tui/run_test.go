package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/dispatch"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

func TestResolveWalkEmptyCache(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveWalk(WalkConfig{StateDir: dir, Scope: Scope{Kind: ScopeAll}})
	if !errors.Is(err, ErrEmptyCache) {
		t.Fatalf("empty cache error = %v, want ErrEmptyCache", err)
	}
}

func TestResolveWalkResolvesCards(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(sources.Task{ID: "1", Title: "one", Project: "Kong"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(sources.Task{ID: "2", Title: "two", Project: "Home"}); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveWalk(WalkConfig{StateDir: dir, Scope: Scope{Kind: ScopeProject, Value: "Kong"}, ScriptsDir: "/scripts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Cards) != 1 || got.Cards[0].TaskID != "1" {
		t.Fatalf("cards = %+v, want the single Kong task", got.Cards)
	}
}

func TestResolveWalkSurfacesAssessment(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveTask(sources.Task{ID: "1", Title: "one", Project: "Kong"})
	_ = st.SaveTask(sources.Task{ID: "2", Title: "two", Project: "Kong"})
	// A succeeded dispatch status for task 1 carries a summary; task 2 has none.
	if err := dispatch.WriteStatus(dir, dispatch.Status{TaskID: "1", State: dispatch.StateSucceeded, Assessment: "waiting on legal"}); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveWalk(WalkConfig{StateDir: dir, Scope: Scope{Kind: ScopeProject, Value: "Kong"}, ScriptsDir: "/scripts"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Card{}
	for _, c := range got.Cards {
		byID[c.TaskID] = c
	}
	if byID["1"].Assessment != "waiting on legal" {
		t.Errorf("card 1 assessment = %q, want %q", byID["1"].Assessment, "waiting on legal")
	}
	if byID["2"].Assessment != "" {
		t.Errorf("card 2 assessment = %q, want empty", byID["2"].Assessment)
	}
}

func TestResolveWalkScopeEmpty(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveTask(sources.Task{ID: "1", Title: "one", Project: "Home"})
	_, err := ResolveWalk(WalkConfig{StateDir: dir, Scope: Scope{Kind: ScopeProject, Value: "Kong"}, ScriptsDir: "/scripts"})
	if !errors.Is(err, ErrScopeEmpty) {
		t.Fatalf("scope-empty error = %v, want ErrScopeEmpty", err)
	}
}

func TestScopeLabelSanitizesValue(t *testing.T) {
	// The scope value reaches stderr, so an embedded escape or newline must not
	// survive into the error message.
	got := scopeLabel(Scope{Kind: ScopeFilter, Value: "@a\x1b[31m\nevil"})
	if strings.ContainsAny(got, "\x1b\n") {
		t.Errorf("scopeLabel kept a control byte: %q", got)
	}
}
