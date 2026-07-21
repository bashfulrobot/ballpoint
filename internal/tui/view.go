package tui

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// Layout constants. The header and footer are fixed height; the body viewport
// flexes to fill the rest, so lipgloss owns wrapping and there is no manual
// cursor math.
const (
	headerLines = 4
	actionLines = 2
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	movedStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Faint(true)
)

// layout sizes the body viewport from the current window. Footer height grows
// with the number of freshness lines so a link-heavy card still shows its body.
func (m *Model) layout() {
	footer := actionLines
	if c, ok := m.current(); ok {
		footer += len(c.Links)
	}
	body := m.height - headerLines - footer
	if body < 1 {
		body = 1
	}
	w := m.width
	if w < 1 {
		w = 80
	}
	m.viewport.Width = w
	m.viewport.Height = body
}

// renderCard renders the current task's work-log Markdown into the viewport.
func (m *Model) renderCard() {
	c, ok := m.current()
	if !ok {
		m.viewport.SetContent("")
		return
	}
	m.viewport.SetContent(renderMarkdown(workLogMarkdown(c), m.viewport.Width))
	m.viewport.GotoTop()
}

// workLogMarkdown builds the Markdown document for a card: the description plus
// each work-log comment with its time and any attachment. The comments are the
// work log (see td_worklog.sh), so they render verbatim through glamour.
func workLogMarkdown(c Card) string {
	var b strings.Builder
	if d := strings.TrimSpace(c.Task.Description); d != "" {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	if len(c.Task.Comments) == 0 {
		if b.Len() == 0 {
			return "_No description or work log yet._"
		}
		return b.String()
	}
	b.WriteString("## Work log\n\n")
	for _, cm := range c.Task.Comments {
		b.WriteString("### ")
		b.WriteString(cm.PostedAt.Format("2006-01-02 15:04"))
		if cm.Attachment != "" {
			b.WriteString(" · ")
			b.WriteString(cm.Attachment)
		}
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(cm.Content))
		b.WriteString("\n\n")
	}
	return b.String()
}

// renderMarkdown renders through glamour at a fixed dark style so the output is
// deterministic and never probes the terminal. On any error it falls back to the
// raw Markdown rather than showing nothing.
func renderMarkdown(md string, width int) string {
	if width < 1 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(width))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

// View composes the four regions. It is a pure function of state, so what the
// tests drive through Update is exactly what renders.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

func (m Model) headerView() string {
	c, ok := m.current()
	if !ok {
		return headerStyle.Render("No tasks in scope")
	}
	t := c.Task
	// Title, project, section, and due are collaborator-controllable and do not
	// pass through glamour (only the body does), so sanitize them before they
	// reach the terminal to strip escape sequences.
	title := headerStyle.Render(sanitizeTerminal(t.Title))
	meta := []string{}
	if t.Project != "" {
		loc := sanitizeTerminal(t.Project)
		if t.Section != "" {
			loc += " / " + sanitizeTerminal(t.Section)
		}
		meta = append(meta, loc)
	}
	if t.Priority != "" {
		meta = append(meta, sanitizeTerminal(t.Priority))
	}
	if t.Due != "" {
		meta = append(meta, "due "+sanitizeTerminal(t.Due))
	}
	pos := fmt.Sprintf("[%d/%d]", m.cursor+1, len(m.cards))
	line2 := dimStyle.Render(strings.Join(meta, "  ·  "))
	return fmt.Sprintf("%s %s\n%s", pos, title, line2)
}

// sanitizeTerminal drops control bytes that could carry ANSI or OSC escape
// sequences from collaborator-controlled text or script output, keeping only
// tab and newline. It neutralizes cursor manipulation, title rewriting, and OSC
// hyperlink or clipboard tricks in text the operator did not author.
func sanitizeTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		case r >= 0x80 && r <= 0x9f:
			return -1
		default:
			return r
		}
	}, s)
}

func (m Model) footerView() string {
	if m.help {
		return m.helpView()
	}
	var b strings.Builder
	if c, ok := m.current(); ok {
		for _, l := range c.Links {
			b.WriteString(freshnessLine(l))
			b.WriteString("\n")
		}
	}
	switch {
	case m.confirming:
		fmt.Fprintf(&b, "%s %q? [y/n]", m.confirmVerb.Name, sanitizeTerminal(m.confirmArg))
	case m.prompting:
		b.WriteString(m.prompt.View())
	default:
		b.WriteString(dimStyle.Render(actionLegend()))
	}
	if m.status != "" {
		// The status line can carry a macro script's raw output, which may echo
		// collaborator-controlled task content, so sanitize it too.
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(sanitizeTerminal(m.status)))
	}
	return b.String()
}

func freshnessLine(l LinkLine) string {
	switch l.State {
	case LinkMoved:
		return movedStyle.Render(fmt.Sprintf("%s: moved", l.System))
	case LinkUnchecked:
		return dimStyle.Render(fmt.Sprintf("%s: unchecked (%s)", l.System, l.Detail))
	default:
		if l.Detail != "" {
			return dimStyle.Render(fmt.Sprintf("%s: %s", l.System, l.Detail))
		}
		return dimStyle.Render(fmt.Sprintf("%s: fresh", l.System))
	}
}

// actionLegend is the one-line key hint. It lists the movement and read-only
// keys; the full lexicon is in the help overlay.
func actionLegend() string {
	return "n next  b back  s skip  l log  d defer  c col  p prio  D done  o open  ? help  q quit"
}

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Keywords"))
	b.WriteString("\n")
	for _, v := range Verbs() {
		if v.Key == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %c  %-9s %s\n", v.Key, v.Name, tierLabel(v.Tier))
	}
	b.WriteString(dimStyle.Render("  outward sends (nudge, email, teams) are queued from draft, never sent here"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  any key to close"))
	return b.String()
}

func tierLabel(t Tier) string {
	switch t {
	case TierInternal:
		return "(logs immediately)"
	case TierCompletion:
		return "(confirms first)"
	case TierNav:
		return ""
	case TierOutward:
		return "(queued)"
	}
	return ""
}

// openInBrowser is the default URL opener. It uses xdg-open and does not wait,
// so opening a link never blocks the walk. The scheme is checked first so a
// cached url can only reach the desktop handler chain when it is http(s), not a
// file:// or custom-handler scheme.
func openInBrowser(rawURL string) error {
	if err := httpOnlyURL(rawURL); err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", rawURL)
	return cmd.Start()
}

// httpOnlyURL rejects any URL whose scheme is not http or https, so a crafted or
// drifted cache value cannot launch an arbitrary desktop handler.
func httpOnlyURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("refusing to open non-http(s) url scheme %q", u.Scheme)
	}
	return nil
}
