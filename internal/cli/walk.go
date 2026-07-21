package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/bashfulrobot/ballpoint/internal/config"
	"github.com/bashfulrobot/ballpoint/internal/store"
	"github.com/bashfulrobot/ballpoint/internal/tui"
)

// walkFlags are the bare command's flags. They live on the top-level FlagSet
// because the walk is the default command, so `ballpoint --project Kong` reaches
// it without a verb token.
type walkFlags struct {
	project    string
	filter     string
	preset     string
	task       string
	scriptsDir string
	refresh    bool
}

// scope turns the flags into a Scope. hasScope is false when no scope flag was
// given, so the caller opens the interactive picker. More than one scope flag is
// an error rather than a silent precedence rule.
func (wf walkFlags) scope() (s tui.Scope, hasScope bool, err error) {
	set := map[string]string{}
	if wf.project != "" {
		set["project"] = wf.project
	}
	if wf.filter != "" {
		set["filter"] = wf.filter
	}
	if wf.preset != "" {
		set["preset"] = wf.preset
	}
	if wf.task != "" {
		set["task"] = wf.task
	}
	if len(set) > 1 {
		return tui.Scope{}, false, errors.New("choose at most one of --project, --filter, --preset, --task")
	}
	scope, ok := tui.ParseScopeFlags(set)
	return scope, ok, nil
}

// runWalk launches the triage-walk TUI. It needs a terminal; scope comes from a
// flag or the interactive picker; a cold cache is refreshed once by shelling out
// to probe.
func runWalk(wf walkFlags, stdout, stderr io.Writer) error {
	// The TUI reads stdin and draws to stdout, so both must be a terminal. A TTY
	// stdout with piped stdin would otherwise pass and then exit instantly on EOF.
	if !isTerminalFile(stdout) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("the triage walk needs an interactive terminal")
	}

	scope, hasScope, err := wf.scope()
	if err != nil {
		return err
	}

	dir, err := config.StateDir()
	if err != nil {
		return err
	}

	if wf.refresh {
		if err := refreshCache(stderr); err != nil {
			return err
		}
	}

	if !hasScope {
		scope, err = pickScope(dir)
		if errors.Is(err, tui.ErrEmptyCache) {
			if err := refreshCache(stderr); err != nil {
				return err
			}
			scope, err = pickScope(dir)
		}
		if err != nil {
			return err
		}
	}

	data, err := tui.ResolveWalk(tui.WalkConfig{StateDir: dir, Scope: scope, ScriptsDir: wf.scriptsDir})
	// Refresh and retry once for a cold cache, or for a single --task id that is
	// not cached yet (a brand-new task), since a probe would bring it in. A
	// project or filter that simply matches nothing does not refresh.
	if !wf.refresh && (errors.Is(err, tui.ErrEmptyCache) ||
		(errors.Is(err, tui.ErrScopeEmpty) && scope.Kind == tui.ScopeTask)) {
		if err := refreshCache(stderr); err != nil {
			return err
		}
		data, err = tui.ResolveWalk(tui.WalkConfig{StateDir: dir, Scope: scope, ScriptsDir: wf.scriptsDir})
	}
	if err != nil {
		return err
	}

	return tui.Run(data)
}

// isTerminalFile reports whether w is a terminal. The TUI cannot run to a pipe
// or a test buffer, so a non-terminal writer fails fast rather than hanging.
func isTerminalFile(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// refreshCache runs `ballpoint probe` in-process by re-invoking this binary, so
// a cold or stale cache is not a dead end. The probe prints its JSON report to
// stdout, which is discarded here; only the cache side effect matters.
func refreshCache(stderr io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating ballpoint to refresh the cache: %w", err)
	}
	_, _ = fmt.Fprintln(stderr, "refreshing the cache (ballpoint probe)...")
	cmd := exec.Command(exe, "probe")
	cmd.Stdout = io.Discard
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("refreshing the cache via probe: %w", err)
	}
	return nil
}

// pickScope opens a project picker over the cached corpus. An empty cache is
// ErrEmptyCache so the caller can refresh and retry.
func pickScope(dir string) (tui.Scope, error) {
	st, err := store.Open(dir)
	if err != nil {
		return tui.Scope{}, err
	}
	tasks, err := st.LoadAllTasks()
	if err != nil {
		return tui.Scope{}, err
	}
	if len(tasks) == 0 {
		return tui.Scope{}, tui.ErrEmptyCache
	}

	seen := map[string]bool{}
	var projects []string
	for _, t := range tasks {
		if t.Project != "" && !seen[t.Project] {
			seen[t.Project] = true
			projects = append(projects, t.Project)
		}
	}
	sort.Strings(projects)

	options := []huh.Option[string]{huh.NewOption("(all tasks)", "")}
	for _, p := range projects {
		options = append(options, huh.NewOption(p, p))
	}

	var chosen string
	if err := huh.NewSelect[string]().
		Title("Walk which project?").
		Options(options...).
		Value(&chosen).
		Run(); err != nil {
		return tui.Scope{}, err
	}
	if chosen == "" {
		return tui.Scope{Kind: tui.ScopeAll}, nil
	}
	return tui.Scope{Kind: tui.ScopeProject, Value: chosen}, nil
}
