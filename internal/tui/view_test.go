package tui

import (
	"strings"
	"testing"
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
