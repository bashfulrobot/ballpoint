package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// ErrEmptyCache means the cache holds no tasks at all. The caller treats it as
// "cold cache" and can refresh (shell out to probe) before retrying.
var ErrEmptyCache = errors.New("no tasks in the cache")

// ErrScopeEmpty means the cache has tasks but none match the scope. Refreshing
// would not help, so the caller reports it rather than probing.
var ErrScopeEmpty = errors.New("no cached tasks match the scope")

// WalkConfig is the input to ResolveWalk: where the cache lives, what to walk,
// and the injectable seams tests use.
type WalkConfig struct {
	StateDir   string
	Scope      Scope
	ScriptsDir string
	Now        time.Time
	OpenURL    func(url string) error
}

// WalkData is a fully resolved walk, ready to drive a Model. It is the output of
// ResolveWalk and the input to Run, so the pure resolution is testable without a
// terminal.
type WalkData struct {
	Cards     []Card
	Scope     Scope
	Report    probe.Report
	Store     *store.Store
	StateRoot string
	Macro     Macro
	Now       time.Time
	OpenURL   func(url string) error
}

// ResolveWalk reads the cache, resolves the scope to cards, and sorts moved-first.
// It never fetches. An empty cache is ErrEmptyCache; a non-empty cache that the
// scope filters to nothing is ErrScopeEmpty.
func ResolveWalk(cfg WalkConfig) (WalkData, error) {
	st, err := store.Open(cfg.StateDir)
	if err != nil {
		return WalkData{}, err
	}
	tasks, err := st.LoadAllTasks()
	if err != nil {
		return WalkData{}, err
	}
	if len(tasks) == 0 {
		return WalkData{}, ErrEmptyCache
	}
	report, _, err := st.LoadReport()
	if err != nil {
		return WalkData{}, err
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	wanted := make(map[string]bool)
	for _, id := range cfg.Scope.Resolve(tasks) {
		wanted[id] = true
	}
	var cards []Card
	for _, tk := range tasks {
		if wanted[tk.ID] {
			cards = append(cards, BuildCard(tk, report.Tasks[tk.ID], now))
		}
	}
	if len(cards) == 0 {
		return WalkData{}, fmt.Errorf("%w: %s", ErrScopeEmpty, scopeLabel(cfg.Scope))
	}
	SortMovedFirst(cards)

	scriptsDir := cfg.ScriptsDir
	if scriptsDir == "" {
		scriptsDir, err = DefaultScriptsDir()
		if err != nil {
			return WalkData{}, err
		}
	}

	return WalkData{
		Cards:     cards,
		Scope:     cfg.Scope,
		Report:    report,
		Store:     st,
		StateRoot: cfg.StateDir,
		Macro:     NewMacro(scriptsDir),
		Now:       now,
		OpenURL:   cfg.OpenURL,
	}, nil
}

// Run builds the Model from resolved data, restores the cursor for a matching
// resumed scope, and runs the Bubbletea program. It requires a terminal; the
// caller is responsible for that check.
func Run(d WalkData) error {
	m := NewModel(Config{
		Cards:     d.Cards,
		Scope:     d.Scope,
		Report:    d.Report,
		Store:     d.Store,
		StateRoot: d.StateRoot,
		Macro:     d.Macro,
		Now:       d.Now,
		OpenURL:   d.OpenURL,
	})

	if s, ok, _ := LoadSession(d.StateRoot); ok && s.Scope == d.Scope {
		order := make([]string, len(d.Cards))
		for i, c := range d.Cards {
			order[i] = c.TaskID
		}
		m.cursor = ResolveCursor(order, s.Cursor)
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("running the triage walk: %w", err)
	}
	return nil
}

// scopeLabel renders a scope for an error message.
func scopeLabel(s Scope) string {
	switch s.Kind {
	case ScopeProject:
		return "project " + s.Value
	case ScopeFilter:
		return "filter " + s.Value
	case ScopePreset:
		return "preset " + s.Value
	case ScopeTask:
		return "task " + s.Value
	default:
		return "the current scope"
	}
}
