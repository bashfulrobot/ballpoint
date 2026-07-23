package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bashfulrobot/ballpoint/internal/dispatch"
	"github.com/bashfulrobot/ballpoint/internal/sanitize"
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

// ResolveWalk reads the cache, resolves the scope to cards, and sorts moved-first.
// It never fetches. An empty cache is ErrEmptyCache; a non-empty cache that the
// scope filters to nothing is ErrScopeEmpty. The returned Config is ready to
// drive a Model, so this pure resolution is testable without a terminal.
func ResolveWalk(cfg WalkConfig) (Config, error) {
	st, err := store.Open(cfg.StateDir)
	if err != nil {
		return Config{}, err
	}
	tasks, err := st.LoadAllTasks()
	if err != nil {
		return Config{}, err
	}
	if len(tasks) == 0 {
		return Config{}, ErrEmptyCache
	}
	report, _, err := st.LoadReport()
	if err != nil {
		return Config{}, err
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}

	// The dispatcher's per-task assessment summaries, resolved locally so a card
	// can show the AI's take ahead of time. A missing dispatch dir is empty, not
	// an error, and a status write failure never blocks the walk, so a read
	// failure here degrades to no assessments rather than failing the walk.
	assessments := map[string]string{}
	if statuses, serr := dispatch.LoadStatuses(cfg.StateDir); serr == nil {
		for _, s := range statuses {
			if s.Assessment != "" {
				assessments[s.TaskID] = s.Assessment
			}
		}
	}

	wanted := make(map[string]bool)
	for _, id := range cfg.Scope.Resolve(tasks) {
		wanted[id] = true
	}
	var cards []Card
	for _, tk := range tasks {
		if wanted[tk.ID] {
			card := BuildCard(tk, report.Tasks[tk.ID], now)
			card.Assessment = assessments[tk.ID]
			cards = append(cards, card)
		}
	}
	if len(cards) == 0 {
		return Config{}, fmt.Errorf("%w: %s", ErrScopeEmpty, scopeLabel(cfg.Scope))
	}
	SortMovedFirst(cards)

	scriptsDir := cfg.ScriptsDir
	if scriptsDir == "" {
		scriptsDir, err = DefaultScriptsDir()
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
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

// Run builds the Model from a resolved Config, restores the cursor for a
// matching resumed scope, and runs the Bubbletea program. It requires a
// terminal; the caller is responsible for that check.
func Run(cfg Config) error {
	m := NewModel(cfg)

	if s, ok, _ := LoadSession(cfg.StateRoot); ok && s.Scope == cfg.Scope {
		order := make([]string, len(cfg.Cards))
		for i, c := range cfg.Cards {
			order[i] = c.TaskID
		}
		m.cursor = ResolveCursor(order, s.Cursor)
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		return fmt.Errorf("running the triage walk: %w", err)
	}
	return nil
}

// scopeLabel renders a scope for an error message. The value is user-supplied
// (a flag or preset name) and lands on stderr, so it is sanitized as a single
// line, the same policy the TUI header uses, to keep an embedded escape sequence
// or newline out of the terminal.
func scopeLabel(s Scope) string {
	v := sanitize.Line(s.Value)
	switch s.Kind {
	case ScopeProject:
		return "project " + v
	case ScopeFilter:
		return "filter " + v
	case ScopePreset:
		return "preset " + v
	case ScopeTask:
		return "task " + v
	default:
		return "the current scope"
	}
}
