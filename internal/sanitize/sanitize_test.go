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

// Zero-width characters and the BOM must be dropped, so hidden runs cannot break
// up an identifier or smuggle invisible content.
func TestStripsZeroWidthAndBOM(t *testing.T) {
	zw := []rune{0x200b, 0x200c, 0x200d, 0xfeff}
	for _, r := range zw {
		in := "no" + string(r) + "gap"
		if got := Line(in); got != "nogap" {
			t.Errorf("Line did not strip zero-width %U: got %q", r, got)
		}
	}
}

func TestKeepsOrdinaryUnicode(t *testing.T) {
	in := "café résumé 日本語 emoji 🙂"
	if got := Block(in); got != in {
		t.Errorf("Block mangled ordinary Unicode: %q", got)
	}
}
