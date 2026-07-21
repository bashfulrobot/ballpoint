package tui

import "testing"

func TestSessionRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, ok, _ := LoadSession(root); ok {
		t.Fatal("empty root reported a session")
	}
	want := Session{Scope: Scope{Kind: ScopeProject, Value: "Kong"}, Cursor: "42"}
	if err := SaveSession(root, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadSession(root)
	if err != nil || !ok || got.Cursor != "42" || got.Scope.Value != "Kong" || got.Scope.Kind != ScopeProject {
		t.Fatalf("LoadSession = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestResolveCursorFallback(t *testing.T) {
	order := []string{"a", "b", "c", "d"}
	if got := ResolveCursor(order, "c"); got != 2 {
		t.Errorf("present cursor -> %d, want 2", got)
	}
	if got := ResolveCursor(order, "zzz"); got != 0 {
		t.Errorf("absent cursor -> %d, want 0 (start)", got)
	}
	if got := ResolveCursor(nil, "a"); got != 0 {
		t.Errorf("empty order -> %d, want 0", got)
	}
}
