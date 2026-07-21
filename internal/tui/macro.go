package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Macro shells out to the tested triage macro scripts for mutations. Run is
// injectable so tests capture the argv without executing anything.
type Macro struct {
	Dir string
	Run func(name string, args ...string) ([]byte, error)
}

// NewMacro builds a Macro whose Run executes the script and captures combined
// output, so a failing script's stderr reaches the caller.
func NewMacro(dir string) Macro {
	return Macro{
		Dir: dir,
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
	}
}

// Exec runs a verb's script with the task ref prepended to the parsed arguments.
// A non-zero exit is wrapped with the captured output so the model can show the
// script's own error, not a bare exit code.
func (m Macro) Exec(v Verb, ref string, args []string) error {
	if v.Script == "" {
		return fmt.Errorf("verb %q has no macro script", v.Name)
	}
	name := filepath.Join(m.Dir, v.Script)
	full := append([]string{ref}, args...)
	out, err := m.Run(name, full...)
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("%s: %w: %s", v.Name, err, trimmed)
		}
		return fmt.Errorf("%s: %w", v.Name, err)
	}
	return nil
}

// DefaultScriptsDir is where the triage macro scripts live. It expands from the
// home directory rather than hardcoding a user path, so the binary is portable;
// a --scripts-dir flag overrides it.
func DefaultScriptsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "skills", "todoist-triage", "scripts"), nil
}
