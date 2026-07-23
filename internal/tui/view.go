package tui

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/bashfulrobot/ballpoint/internal/sanitize"
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
// each work-log comment with its time and any attachment. Description, comment
// content, and attachment names are collaborator-controlled, and glamour does
// not strip control bytes, so each is sanitized before it enters the markdown.
func workLogMarkdown(c Card) string {
	var b strings.Builder
	// The dispatcher's assessment leads the card, above the human description and
	// work log. It is model-produced, so it is sanitized like every other body
	// string before glamour renders it. Sanitizing strips control bytes but not
	// markdown, so, exactly like the description and comment bodies below, the
	// text can carry its own markdown; the leading heading marks the section but
	// does not fence untrusted text out of it. Absent assessment renders nothing,
	// no empty heading.
	if a := sanitizeTerminal(strings.TrimSpace(c.Assessment)); a != "" {
		b.WriteString("## Assessment\n\n")
		b.WriteString(a)
		b.WriteString("\n\n")
	}
	if d := sanitizeTerminal(strings.TrimSpace(c.Task.Description)); d != "" {
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
			b.WriteString(sanitizeLine(cm.Attachment))
		}
		b.WriteString("\n\n")
		b.WriteString(sanitizeTerminal(strings.TrimSpace(cm.Content)))
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
	// Title, project, section, and due are collaborator-controllable and render
	// on single lines, so sanitizeLine strips escape sequences and collapses any
	// embedded newline that would otherwise inject extra header lines.
	title := headerStyle.Render(sanitizeLine(t.Title))
	meta := []string{}
	if t.Project != "" {
		loc := sanitizeLine(t.Project)
		if t.Section != "" {
			loc += " / " + sanitizeLine(t.Section)
		}
		meta = append(meta, loc)
	}
	if t.Priority != "" {
		meta = append(meta, sanitizeLine(t.Priority))
	}
	if t.Due != "" {
		meta = append(meta, "due "+sanitizeLine(t.Due))
	}
	pos := fmt.Sprintf("[%d/%d]", m.cursor+1, len(m.cards))
	line2 := dimStyle.Render(strings.Join(meta, "  ·  "))
	return fmt.Sprintf("%s %s\n%s", pos, title, line2)
}

// sanitizeTerminal drops control bytes and dangerous Unicode that could carry
// ANSI or OSC escape sequences, or reorder text, from collaborator-controlled
// text, keeping tab and newline. Use it for multi-line body text (fed to
// glamour, which does not strip control bytes of its own). It neutralizes cursor
// manipulation, title rewriting, OSC hyperlink or clipboard tricks, and
// Trojan-Source bidi reordering in text the operator did not author.
func sanitizeTerminal(s string) string { return sanitize.Block(s) }

// sanitizeLine is sanitizeTerminal for single-line contexts (the header fields
// and the status line). It also collapses tab and newline to a space, so a title
// carrying newlines cannot inject extra lines and spoof the fixed-height header.
func sanitizeLine(s string) string { return sanitize.Line(s) }

// SanitizeLabel strips control bytes from a single-line label. The CLI scope
// picker renders collaborator-controlled project names before the TUI starts, so
// it sanitizes each label with the same policy the header uses.
func SanitizeLabel(s string) string { return sanitize.Line(s) }

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
		fmt.Fprintf(&b, "%s %q? [y/n]", m.confirmVerb.Name, sanitizeLine(m.confirmArg))
	case m.prompting:
		b.WriteString(m.prompt.View())
	default:
		b.WriteString(dimStyle.Render(actionLegend()))
	}
	if m.status != "" {
		// The status line can carry a macro script's raw output, which may echo
		// collaborator-controlled task content, so sanitize it as a single line.
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(sanitizeLine(m.status)))
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
