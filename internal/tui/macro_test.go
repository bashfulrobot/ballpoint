package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestMacroExecArgv(t *testing.T) {
	var gotName string
	var gotArgs []string
	m := Macro{Dir: "/s", Run: func(name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return nil, nil
	}}
	err := m.Exec(Verb{Name: "log", Script: "td_worklog.sh"}, "DEVP-I-42", []string{"--entry", "chased eng"})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "/s/td_worklog.sh" {
		t.Errorf("script = %q, want /s/td_worklog.sh", gotName)
	}
	if len(gotArgs) != 3 || gotArgs[0] != "DEVP-I-42" || gotArgs[1] != "--entry" || gotArgs[2] != "chased eng" {
		t.Errorf("args = %v, want [DEVP-I-42 --entry chased eng]", gotArgs)
	}
}

func TestMacroExecSurfacesStderr(t *testing.T) {
	m := Macro{Dir: "/s", Run: func(name string, args ...string) ([]byte, error) {
		return []byte("boom: bad column\n"), errors.New("exit status 1")
	}}
	err := m.Exec(Verb{Name: "col", Script: "td_move.sh"}, "1", []string{"Nope"})
	if err == nil || !strings.Contains(err.Error(), "boom: bad column") {
		t.Fatalf("Exec() error = %v, want it to carry the script's stderr", err)
	}
}

func TestMacroExecRejectsScriptlessVerb(t *testing.T) {
	m := Macro{Dir: "/s", Run: func(string, ...string) ([]byte, error) { return nil, nil }}
	if err := m.Exec(Verb{Name: "next"}, "1", nil); err == nil {
		t.Error("Exec() on a verb with no script should error")
	}
}
