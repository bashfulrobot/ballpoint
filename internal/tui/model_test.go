package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// macroRecorder captures every macro invocation so tests assert what ran (or
// that nothing did) without shelling out.
type macroRecorder struct {
	calls [][]string
}

func (r *macroRecorder) run(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, nil
}

func newTestModelAt(t *testing.T, root string, n int) (Model, *macroRecorder) {
	t.Helper()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cards := make([]Card, 0, n)
	for i := 0; i < n; i++ {
		id := string(rune('1' + i))
		task := sources.Task{ID: id, Title: "task " + id, Project: "Kong"}
		if err := st.SaveTask(task); err != nil {
			t.Fatal(err)
		}
		cards = append(cards, BuildCard(task, probe.TaskReport{}, time.Unix(0, 0)))
	}
	rec := &macroRecorder{}
	m := NewModel(Config{
		Cards:     cards,
		Scope:     Scope{Kind: ScopeProject, Value: "Kong"},
		Store:     st,
		StateRoot: root,
		Macro:     Macro{Dir: "/scripts", Run: rec.run},
		Now:       time.Unix(1, 0).UTC(),
		OpenURL:   func(string) error { return nil },
	})
	return m, rec
}

func newTestModel(t *testing.T, n int) (Model, *macroRecorder) {
	t.Helper()
	return newTestModelAt(t, t.TempDir(), n)
}

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func enter() tea.KeyMsg     { return tea.KeyMsg{Type: tea.KeyEnter} }
func typeInto(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModelNextAdvances(t *testing.T) {
	m, _ := newTestModel(t, 3)
	out, _ := m.Update(key('n'))
	if out.(Model).cursor != 1 {
		t.Errorf("cursor after next = %d, want 1", out.(Model).cursor)
	}
}

func TestModelBackClampsAtStart(t *testing.T) {
	m, _ := newTestModel(t, 3)
	out, _ := m.Update(key('b'))
	if out.(Model).cursor != 0 {
		t.Errorf("cursor after back at start = %d, want 0", out.(Model).cursor)
	}
}

func TestModelInternalVerbOpensPrompt(t *testing.T) {
	m, _ := newTestModel(t, 1)
	out, _ := m.Update(key('l'))
	if !out.(Model).prompting {
		t.Error("log should open the argument prompt")
	}
	if out.(Model).pendingVerb.Name != "log" {
		t.Errorf("pending verb = %q, want log", out.(Model).pendingVerb.Name)
	}
}

func TestModelPromptRunsMacro(t *testing.T) {
	m, rec := newTestModel(t, 1)
	out, _ := m.Update(key('l')) // open prompt
	out, _ = out.(Model).Update(typeInto("chased eng"))
	out, _ = out.(Model).Update(enter())
	m2 := out.(Model)
	if m2.prompting {
		t.Error("prompt should close after Enter")
	}
	if len(rec.calls) != 1 {
		t.Fatalf("macro calls = %d, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	want := []string{"/scripts/td_worklog.sh", "id:1", "--entry", "chased eng"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}

func TestModelCompletionConfirms(t *testing.T) {
	m, rec := newTestModel(t, 1)
	out, _ := m.Update(key('D')) // done -> prompt
	out, _ = out.(Model).Update(typeInto("shipped"))
	out, _ = out.(Model).Update(enter())
	m2 := out.(Model)
	if !m2.confirming {
		t.Fatal("completion verb should open the confirm gate, not run immediately")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("macro ran before confirm: %v", rec.calls)
	}
	m2.Update(key('y'))
	if len(rec.calls) != 1 {
		t.Fatalf("macro calls after confirm = %d, want 1", len(rec.calls))
	}
}

func TestModelCompletionConfirmCancel(t *testing.T) {
	m, rec := newTestModel(t, 1)
	out, _ := m.Update(key('X')) // drop
	out, _ = out.(Model).Update(typeInto("no longer relevant"))
	out, _ = out.(Model).Update(enter())
	out, _ = out.(Model).Update(key('n')) // decline
	if out.(Model).confirming {
		t.Error("declining should close the confirm gate")
	}
	if len(rec.calls) != 0 {
		t.Errorf("declined completion ran the macro: %v", rec.calls)
	}
}

func TestModelOutwardQueuesNeverSends(t *testing.T) {
	root := t.TempDir()
	m, rec := newTestModelAt(t, root, 1)
	out, _ := m.Update(key('r')) // draft
	out, _ = out.(Model).Update(typeInto("nudge #team ping the owner"))
	out, _ = out.(Model).Update(enter())
	entries, err := queue.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("queue has %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Channel != "nudge" || e.To != "#team" || e.Body != "ping the owner" || e.TaskID != "1" {
		t.Errorf("queued entry = %+v", e)
	}
	if len(rec.calls) != 0 {
		t.Errorf("outward action shelled out instead of queuing: %v", rec.calls)
	}
	_ = out
}

func TestModelDraftWithoutChannelDoesNothing(t *testing.T) {
	root := t.TempDir()
	m, rec := newTestModelAt(t, root, 1)
	out, _ := m.Update(key('r'))
	out, _ = out.(Model).Update(typeInto("remember to follow up"))
	out.(Model).Update(enter())
	if len(rec.calls) != 0 {
		t.Errorf("draft with no channel shelled out: %v", rec.calls)
	}
	entries, _ := queue.Load(root)
	if len(entries) != 0 {
		t.Errorf("draft with no channel queued %d entries, want 0", len(entries))
	}
}

func TestModelDraftEmptyRecipientRejected(t *testing.T) {
	root := t.TempDir()
	m, _ := newTestModelAt(t, root, 1)
	out, _ := m.Update(key('r'))
	out, _ = out.(Model).Update(typeInto("nudge"))
	out.(Model).Update(enter())
	entries, _ := queue.Load(root)
	if len(entries) != 0 {
		t.Errorf("channel with no recipient queued %d entries, want 0", len(entries))
	}
}

func TestModelDraftEmptyBodyRejected(t *testing.T) {
	root := t.TempDir()
	m, _ := newTestModelAt(t, root, 1)
	out, _ := m.Update(key('r'))
	out, _ = out.(Model).Update(typeInto("nudge #team"))
	out.(Model).Update(enter())
	entries, _ := queue.Load(root)
	if len(entries) != 0 {
		t.Errorf("channel and recipient but no body queued %d entries, want 0", len(entries))
	}
}

func TestModelConfirmEscCancels(t *testing.T) {
	m, rec := newTestModel(t, 1)
	out, _ := m.Update(key('D'))
	out, _ = out.(Model).Update(typeInto("shipped"))
	out, _ = out.(Model).Update(enter())
	out, _ = out.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).confirming {
		t.Error("Esc should close the confirm gate")
	}
	if len(rec.calls) != 0 {
		t.Errorf("Esc-cancelled completion ran the macro: %v", rec.calls)
	}
}

func TestModelPromptEscCancels(t *testing.T) {
	m, rec := newTestModel(t, 1)
	out, _ := m.Update(key('l'))
	out, _ = out.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).prompting {
		t.Error("Esc should close the prompt")
	}
	if len(rec.calls) != 0 {
		t.Errorf("cancelled prompt ran a macro: %v", rec.calls)
	}
}

func TestModelResizeReflows(t *testing.T) {
	m, _ := newTestModel(t, 1)
	out, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m2 := out.(Model)
	if m2.width != 40 {
		t.Errorf("width after resize = %d, want 40", m2.width)
	}
	if m2.viewport.Width != 40 {
		t.Errorf("viewport width = %d, want 40", m2.viewport.Width)
	}
}

func TestModelQuitReturnsQuitCmd(t *testing.T) {
	m, _ := newTestModel(t, 1)
	_, cmd := m.Update(key('q'))
	if cmd == nil {
		t.Fatal("quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("quit command did not produce tea.QuitMsg")
	}
}

func TestModelHelpToggles(t *testing.T) {
	m, _ := newTestModel(t, 1)
	out, _ := m.Update(key('?'))
	if !out.(Model).help {
		t.Fatal("? should open help")
	}
	out, _ = out.(Model).Update(key('n'))
	if out.(Model).help {
		t.Error("any key should close help")
	}
}

func TestModelSessionPersistsOnMove(t *testing.T) {
	root := t.TempDir()
	m, _ := newTestModelAt(t, root, 3)
	out, _ := m.Update(key('n'))
	_ = out
	s, ok, err := LoadSession(root)
	if err != nil || !ok {
		t.Fatalf("session not written: ok=%v err=%v", ok, err)
	}
	if s.Cursor != "2" {
		t.Errorf("session cursor = %q, want 2 (second card)", s.Cursor)
	}
}
