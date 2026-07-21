package tui

import (
	"errors"
	"testing"

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

func TestResolveWalkScopeEmpty(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	_ = st.SaveTask(sources.Task{ID: "1", Title: "one", Project: "Home"})
	_, err := ResolveWalk(WalkConfig{StateDir: dir, Scope: Scope{Kind: ScopeProject, Value: "Kong"}, ScriptsDir: "/scripts"})
	if !errors.Is(err, ErrScopeEmpty) {
		t.Fatalf("scope-empty error = %v, want ErrScopeEmpty", err)
	}
}
