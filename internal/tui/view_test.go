package tui

import (
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestSanitizeTerminalStripsEscapes(t *testing.T) {
	// A title carrying an ESC-based ANSI sequence and an OSC title-set string.
	in := "fix \x1b[31mred\x1b[0m \x1b]0;pwned\x07 task\ttab\nnewline"
	got := sanitizeTerminal(in)
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x07) {
		t.Fatalf("sanitizeTerminal left control bytes: %q", got)
	}
	if !strings.Contains(got, "\t") || !strings.Contains(got, "\n") {
		t.Errorf("sanitizeTerminal dropped tab or newline: %q", got)
	}
	if !strings.Contains(got, "fix ") || !strings.Contains(got, "task") {
		t.Errorf("sanitizeTerminal dropped printable text: %q", got)
	}
}

func TestSanitizeTerminalDropsC1(t *testing.T) {
	// U+009B is the C1 control (CSI); Todoist returns UTF-8, so it arrives as a
	// proper rune, not a raw byte.
	in := string([]rune{'a', 0x9b, 'b'})
	if got := sanitizeTerminal(in); got != "ab" {
		t.Errorf("sanitizeTerminal(C1) = %q, want ab", got)
	}
}

func TestSanitizeLineCollapsesNewlines(t *testing.T) {
	got := sanitizeLine("Ship it\n\n\n [y/n] approve?")
	if strings.ContainsRune(got, '\n') || strings.ContainsRune(got, '\t') {
		t.Errorf("sanitizeLine kept a newline or tab: %q", got)
	}
	if !strings.Contains(got, "Ship it") || !strings.Contains(got, "approve?") {
		t.Errorf("sanitizeLine dropped printable text: %q", got)
	}
}

func TestWorkLogMarkdownSanitizesBody(t *testing.T) {
	c := Card{Task: sources.Task{
		Description: "desc \x1b]0;pwned\x07 body",
		Comments: []sources.Comment{
			{Content: "log \x1b]52;c;AAAA\x07 entry", Attachment: "file\x1b]0;x\x07.pdf"},
		},
	}}
	md := workLogMarkdown(c)
	if strings.ContainsRune(md, 0x1b) || strings.ContainsRune(md, 0x07) {
		t.Fatalf("workLogMarkdown left control bytes in the body: %q", md)
	}
}

func TestWorkLogMarkdownAssessment(t *testing.T) {
	// A card with an assessment leads with a sanitized Assessment section.
	c := Card{Assessment: "stale, ping the owner \x1b]0;x\x07", Task: sources.Task{Description: "the task"}}
	md := workLogMarkdown(c)
	if !strings.Contains(md, "## Assessment") {
		t.Errorf("markdown missing the Assessment heading: %q", md)
	}
	if !strings.Contains(md, "stale, ping the owner") {
		t.Errorf("markdown missing the assessment summary: %q", md)
	}
	if strings.ContainsRune(md, 0x1b) || strings.ContainsRune(md, 0x07) {
		t.Fatalf("assessment left control bytes in the body: %q", md)
	}
	if strings.Index(md, "## Assessment") > strings.Index(md, "the task") {
		t.Errorf("assessment should lead the description: %q", md)
	}

	// A card with no assessment renders no heading, exactly as before.
	c2 := Card{Task: sources.Task{Description: "the task"}}
	if md2 := workLogMarkdown(c2); strings.Contains(md2, "Assessment") {
		t.Errorf("empty assessment must render no heading: %q", md2)
	}
}

func TestHTTPOnlyURL(t *testing.T) {
	for _, ok := range []string{"https://todoist.com/showTask?id=1", "http://x.test"} {
		if err := httpOnlyURL(ok); err != nil {
			t.Errorf("httpOnlyURL(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "ftp://x", "vscode://x"} {
		if err := httpOnlyURL(bad); err == nil {
			t.Errorf("httpOnlyURL(%q) = nil, want rejection", bad)
		}
	}
}
