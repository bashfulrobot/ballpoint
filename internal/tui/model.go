package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// Config builds a Model. resolveWalk (run.go) fills it from the cache; tests
// build it directly with in-memory cards and an injected macro runner.
type Config struct {
	Cards     []Card
	Scope     Scope
	Report    probe.Report
	Store     *store.Store
	StateRoot string
	Macro     Macro
	Now       time.Time
	OpenURL   func(url string) error // injected so tests never spawn a browser
}

// Model is the triage-walk TUI. It composes the pure core (cards, keymap, macro
// runner, queue, session) and is driven entirely through Update, so its
// behaviour is unit-testable without a terminal.
type Model struct {
	cards     []Card
	cursor    int
	width     int
	height    int
	viewport  viewport.Model
	report    probe.Report
	scope     Scope
	stateRoot string
	store     *store.Store
	macro     Macro
	now       time.Time
	openURL   func(url string) error

	prompting   bool
	prompt      textinput.Model
	pendingVerb Verb

	confirming  bool
	confirmVerb Verb
	confirmArg  string

	help     bool
	status   string
	quitting bool
	seq      int // monotonic suffix for queue entry ids this session
}

// NewModel builds the model. A zero OpenURL is replaced with the real opener so
// callers that do not care about the injection get working behaviour.
func NewModel(cfg Config) Model {
	ti := textinput.New()
	ti.Prompt = "> "
	m := Model{
		cards:     cfg.Cards,
		report:    cfg.Report,
		scope:     cfg.Scope,
		stateRoot: cfg.StateRoot,
		store:     cfg.Store,
		macro:     cfg.Macro,
		now:       cfg.Now,
		openURL:   cfg.OpenURL,
		viewport:  viewport.New(0, 0),
		prompt:    ti,
	}
	if m.openURL == nil {
		m.openURL = openInBrowser
	}
	if m.now.IsZero() {
		m.now = time.Now()
	}
	return m
}

// Init satisfies tea.Model. The first WindowSizeMsg lays out and renders the
// opening card, so there is nothing to do up front.
func (m Model) Init() tea.Cmd { return nil }

// Update is the whole state machine. It routes by mode (confirm, prompt, help)
// before falling through to verb dispatch.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		m.renderCard()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.confirming:
		return m.handleConfirm(msg)
	case m.prompting:
		return m.handlePrompt(msg)
	case m.help:
		m.help = false // any key dismisses the overlay
		return m, nil
	default:
		return m.handleVerb(msg)
	}
}

func (m Model) handleVerb(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, nil
	}
	v, ok := VerbForKey(msg.Runes[0])
	if !ok {
		m.status = fmt.Sprintf("no keyword bound to %q", string(msg.Runes[0]))
		return m, nil
	}
	switch v.Tier {
	case TierNav:
		return m.handleNav(v)
	case TierInternal, TierCompletion:
		if v.NeedsArg {
			m.prompting = true
			m.pendingVerb = v
			m.prompt.Reset()
			m.prompt.Placeholder = v.Prompt
			return m, m.prompt.Focus()
		}
		return m.runInternal(v, "")
	}
	return m, nil
}

func (m Model) handleNav(v Verb) (tea.Model, tea.Cmd) {
	switch v.Name {
	case "next", "skip":
		return m.move(1)
	case "back":
		return m.move(-1)
	case "more":
		m.viewport.HalfPageDown()
		return m, nil
	case "dig":
		m.viewport.GotoTop()
		return m, nil
	case "open":
		if c, ok := m.current(); ok && c.Task.URL != "" {
			if err := m.openURL(c.Task.URL); err != nil {
				m.status = "open failed: " + err.Error()
			}
		}
		return m, nil
	case "help":
		m.help = true
		return m, nil
	case "quit":
		m.quitting = true
		m.saveSession()
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) move(delta int) (tea.Model, tea.Cmd) {
	if len(m.cards) == 0 {
		return m, nil
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.cards)-1)
	m.status = ""
	m.renderCard()
	m.saveSession()
	return m, nil
}

func (m Model) handlePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.prompting = false
		m.pendingVerb = Verb{}
		m.prompt.Blur()
		m.status = "cancelled"
		return m, nil
	case tea.KeyEnter:
		v := m.pendingVerb
		arg := strings.TrimSpace(m.prompt.Value())
		m.prompting = false
		m.pendingVerb = Verb{}
		m.prompt.Blur()
		return m.submitArg(v, arg)
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

// submitArg routes a typed argument. A draft whose first token names an outward
// verb is queued, never sent. Completion verbs go through the confirm gate.
// Everything else runs its macro immediately.
func (m Model) submitArg(v Verb, arg string) (tea.Model, tea.Cmd) {
	if arg == "" {
		m.status = v.Name + ": no input, nothing done"
		return m, nil
	}
	if v.Name == "draft" {
		if ov, ok := outwardFromArg(arg); ok {
			return m.queueOutward(ov, arg)
		}
	}
	if v.Tier == TierCompletion {
		m.confirming = true
		m.confirmVerb = v
		m.confirmArg = arg
		m.status = ""
		return m, nil
	}
	return m.runInternal(v, arg)
}

func (m Model) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, nil
	}
	switch msg.Runes[0] {
	case 'y', 'Y':
		v, arg := m.confirmVerb, m.confirmArg
		m.confirming = false
		m.confirmVerb = Verb{}
		m.confirmArg = ""
		return m.runInternal(v, arg)
	case 'n', 'N':
		m.confirming = false
		m.confirmVerb = Verb{}
		m.confirmArg = ""
		m.status = "cancelled"
		return m, nil
	}
	return m, nil
}

// runInternal executes a verb's macro against the current card, then reloads the
// card from cache so a new work-log entry shows. A script failure surfaces its
// stderr in the status line and does not advance.
func (m Model) runInternal(v Verb, arg string) (tea.Model, tea.Cmd) {
	c, ok := m.current()
	if !ok {
		m.status = "no task selected"
		return m, nil
	}
	ref := "id:" + c.TaskID
	if err := m.macro.ExecArgv(v.Name, v.Script, BuildArgv(v, ref, arg)); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.reloadCurrent()
	m.status = fmt.Sprintf("%s done", v.Name)
	return m, nil
}

// queueOutward appends an outward action to the dispatch queue. It never sends;
// issue #6's dispatcher drains the queue. arg is "<verb> <to> <body...>".
func (m Model) queueOutward(v Verb, arg string) (tea.Model, tea.Cmd) {
	c, ok := m.current()
	if !ok {
		m.status = "no task selected"
		return m, nil
	}
	fields := strings.Fields(arg)
	to, body := "", ""
	if len(fields) >= 2 {
		to = fields[1]
	}
	if len(fields) >= 3 {
		body = strings.Join(fields[2:], " ")
	}
	entry := queue.Entry{
		ID:       c.TaskID + "-" + strconv.Itoa(m.seq),
		TaskID:   c.TaskID,
		TaskRef:  "id:" + c.TaskID,
		Channel:  v.Name,
		To:       to,
		Body:     body,
		QueuedAt: m.now,
	}
	if err := queue.Append(m.stateRoot, entry); err != nil {
		m.status = "queue failed: " + err.Error()
		return m, nil
	}
	m.seq++
	m.status = fmt.Sprintf("queued %s for dispatch (not sent)", v.Name)
	return m, nil
}

// reloadCurrent re-derives the current card from the cache after a mutation.
// A read failure leaves the existing card in place rather than blanking it.
func (m *Model) reloadCurrent() {
	c, ok := m.current()
	if !ok || m.store == nil {
		return
	}
	task, found, err := m.store.LoadTask(c.TaskID)
	if err != nil || !found {
		return
	}
	m.cards[m.cursor] = BuildCard(task, m.report.Tasks[c.TaskID], m.now)
	m.renderCard()
}

func (m *Model) saveSession() {
	c, ok := m.current()
	if !ok || m.stateRoot == "" {
		return
	}
	_ = SaveSession(m.stateRoot, Session{Scope: m.scope, Cursor: c.TaskID})
}

func (m Model) current() (Card, bool) {
	if m.cursor < 0 || m.cursor >= len(m.cards) {
		return Card{}, false
	}
	return m.cards[m.cursor], true
}

// outwardFromArg reports whether the first token of a draft argument names an
// outward verb, so "nudge #team ping" queues instead of drafting locally.
func outwardFromArg(arg string) (Verb, bool) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return Verb{}, false
	}
	v, ok := VerbForName(fields[0])
	if !ok || v.Tier != TierOutward {
		return Verb{}, false
	}
	return v, true
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
