// Package sanitize strips bytes that could steer a terminal or a downstream
// reader from collaborator-controlled text. It is the one place ballpoint
// neutralizes escape sequences and Trojan-Source style Unicode, so the TUI, the
// scope picker, and the dispatcher's prompt all share the same policy.
package sanitize

import "strings"

// Line strips dangerous runes from a single-line field and collapses tab and
// newline to a space, so embedded newlines cannot spoof a fixed-height layout
// or inject extra lines.
func Line(s string) string { return strip(s, false) }

// Block strips dangerous runes from multi-line text, keeping tab and newline.
// Use it for body text that is fed to a Markdown renderer or a work-log entry.
func Block(s string) string { return strip(s, true) }

// strip drops C0/C1 control bytes and DEL, the Unicode bidirectional overrides
// and isolates (U+202A..U+202E, U+2066..U+2069, the Trojan-Source vectors from
// CVE-2021-42574), and zero-width characters and the BOM (U+200B..U+200D,
// U+FEFF) that hide or reorder text. The Unicode line and paragraph separators
// (U+2028, U+2029) are treated as line breaks: many Markdown, HTML, and JS
// renderers break on them, so a single-line field must not carry one. When
// keepWhitespace is false, tab, newline, and the separators collapse to a space;
// when true, newline passes through and the separators normalize to newline.
func strip(s string, keepWhitespace bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			if keepWhitespace {
				return r
			}
			return ' '
		case r == 0x2028 || r == 0x2029: // Unicode line and paragraph separators
			if keepWhitespace {
				return '\n'
			}
			return ' '
		case r < 0x20 || r == 0x7f: // C0 controls and DEL
			return -1
		case r >= 0x80 && r <= 0x9f: // C1 controls
			return -1
		case r >= 0x202a && r <= 0x202e: // bidi embeddings and overrides
			return -1
		case r >= 0x2066 && r <= 0x2069: // bidi isolates
			return -1
		case r == 0x200b || r == 0x200c || r == 0x200d || r == 0xfeff: // zero-width, BOM
			return -1
		default:
			return r
		}
	}, s)
}
