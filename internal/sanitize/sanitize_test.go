package sanitize

import "testing"

func TestLineStripsControlAndCollapsesWhitespace(t *testing.T) {
	in := "a\x1b[31mb\tc\nd\x7fe"
	got := Line(in)
	want := "a[31mb c de"
	if got != want {
		t.Errorf("Line(%q) = %q, want %q", in, got, want)
	}
}

func TestBlockKeepsTabAndNewline(t *testing.T) {
	in := "a\tb\nc"
	if got := Block(in); got != in {
		t.Errorf("Block(%q) = %q, want it unchanged", in, got)
	}
}

func TestBlockStripsControlBytes(t *testing.T) {
	// NUL, ESC, and a C1 control (U+009F) built as a rune so it encodes as the
	// two-byte U+009F, not a bare 0x9f that would decode as U+FFFD.
	in := "a\x00b\x1bc" + string(rune(0x9f)) + "d"
	want := "abcd"
	if got := Block(in); got != want {
		t.Errorf("Block(%q) = %q, want %q", in, got, want)
	}
}

// Trojan-Source bidirectional overrides and isolates must be dropped, so text
// cannot be visually reordered to hide or spoof content (CVE-2021-42574).
func TestStripsBidiControls(t *testing.T) {
	bidi := []rune{
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e, // embeddings and overrides
		0x2066, 0x2067, 0x2068, 0x2069, // isolates
	}
	for _, r := range bidi {
		in := "safe" + string(r) + "text"
		if got := Line(in); got != "safetext" {
			t.Errorf("Line did not strip bidi %U: got %q", r, got)
		}
		if got := Block(in); got != "safetext" {
			t.Errorf("Block did not strip bidi %U: got %q", r, got)
		}
	}
}

// Zero-width characters, joiners, directional marks, and the BOM must be
// dropped, so hidden runs cannot break up an identifier, smuggle invisible
// content, or shift local visual order.
func TestStripsZeroWidthAndMarks(t *testing.T) {
	invisible := []rune{
		0x200b, 0x200c, 0x200d, // zero-width space, non-joiner, joiner
		0x200e, 0x200f, // left-to-right and right-to-left marks
		0x061c,         // Arabic letter mark
		0x2060,         // word joiner
		0x2061, 0x2064, // invisible operators (bounds of the range)
		0x180e, // Mongolian vowel separator
		0xfeff, // BOM / zero-width no-break space
	}
	for _, r := range invisible {
		in := "no" + string(r) + "gap"
		if got := Line(in); got != "nogap" {
			t.Errorf("Line did not strip invisible %U: got %q", r, got)
		}
		if got := Block(in); got != "nogap" {
			t.Errorf("Block did not strip invisible %U: got %q", r, got)
		}
	}
}

// The Unicode line and paragraph separators must not survive in a single-line
// field and must normalize to a newline in a block, so they cannot act as a
// hidden line break in a downstream renderer.
func TestStripsLineAndParagraphSeparators(t *testing.T) {
	for _, r := range []rune{0x2028, 0x2029} {
		in := "one" + string(r) + "two"
		if got := Line(in); got != "one two" {
			t.Errorf("Line did not collapse %U: got %q", r, got)
		}
		if got := Block(in); got != "one\ntwo" {
			t.Errorf("Block did not normalize %U to newline: got %q", r, got)
		}
	}
}

func TestKeepsOrdinaryUnicode(t *testing.T) {
	in := "café résumé 日本語 emoji 🙂"
	if got := Block(in); got != in {
		t.Errorf("Block mangled ordinary Unicode: %q", got)
	}
}
