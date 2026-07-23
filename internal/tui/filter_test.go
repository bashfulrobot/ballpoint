package tui

import (
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestSubstringMatch(t *testing.T) {
	task := sources.Task{Title: "Fix the Repro", Labels: []string{"waiting"}, Section: "Inbox"}
	cases := []struct {
		q    string
		want bool
	}{
		{"repro", true},   // title, case-insensitive
		{"waiting", true}, // label
		{"inbox", true},   // section
		{"missing", false},
	}
	for _, c := range cases {
		if got := substringMatch(task, c.q); got != c.want {
			t.Errorf("substringMatch(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}

func TestTokenizeFilter(t *testing.T) {
	cases := []struct {
		in   string
		want []filterToken
	}{
		{"@waiting & #Work", []filterToken{{kind: tokTerm, text: "@waiting"}, {kind: tokAnd}, {kind: tokTerm, text: "#Work"}}},
		{"!@a | @b", []filterToken{{kind: tokNot}, {kind: tokTerm, text: "@a"}, {kind: tokOr}, {kind: tokTerm, text: "@b"}}},
		{"(@a)", []filterToken{{kind: tokLParen}, {kind: tokTerm, text: "@a"}, {kind: tokRParen}}},
		{"#My Project", []filterToken{{kind: tokTerm, text: "#My Project"}}}, // spaces kept inside a term
		{"   ", nil}, // only whitespace yields no tokens
		// A quoted span protects operator characters, so a name with & stays one term.
		{`#"R&D"`, []filterToken{{kind: tokTerm, text: "#R&D"}}},
		{`@"in progress" & #Work`, []filterToken{{kind: tokTerm, text: "@in progress"}, {kind: tokAnd}, {kind: tokTerm, text: "#Work"}}},
	}
	for _, c := range cases {
		got, ok := tokenizeFilter(c.in)
		if !ok {
			t.Errorf("tokenizeFilter(%q) ok=false, want true", c.in)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("tokenizeFilter(%q) = %+v, want %+v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i].kind != c.want[i].kind || got[i].text != c.want[i].text {
				t.Errorf("tokenizeFilter(%q)[%d] = %+v, want %+v", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestTokenizeFilterUnterminatedQuote(t *testing.T) {
	if _, ok := tokenizeFilter(`#"R&D`); ok {
		t.Error(`tokenizeFilter("#\"R&D") ok=true, want false for an unterminated quote`)
	}
}

func TestTermPredicate(t *testing.T) {
	waiting := sources.Task{Labels: []string{"waiting"}, Project: "Work", Priority: "p1", Title: "look at overdue items"}
	other := sources.Task{Labels: []string{"someday"}, Project: "Home", Priority: "p4", Title: "buy milk"}

	cases := []struct {
		term        string
		hit, miss   sources.Task
		wantHitTrue bool
	}{
		{"@Waiting", waiting, other, true}, // label equality, case-insensitive
		{"#work", waiting, other, true},    // project equality, case-insensitive
		{"p1", waiting, other, true},       // priority equality
		{"overdue", waiting, other, true},  // unknown term -> substring on title
	}
	for _, c := range cases {
		pred := termPredicate(c.term)
		if got := pred(c.hit); got != c.wantHitTrue {
			t.Errorf("termPredicate(%q) on hit = %v, want %v", c.term, got, c.wantHitTrue)
		}
		if pred(c.miss) {
			t.Errorf("termPredicate(%q) matched the miss task unexpectedly", c.term)
		}
	}
}

func TestTermPredicateProjectIsNotSubstring(t *testing.T) {
	// #work must be exact on the project name, not a substring, so a project
	// named "Workshop" does not match #work.
	pred := termPredicate("#work")
	if pred(sources.Task{Project: "Workshop"}) {
		t.Error("#work matched project Workshop; project must be exact, not substring")
	}
}

func TestCompileFilterComposition(t *testing.T) {
	a := sources.Task{Labels: []string{"a"}}
	b := sources.Task{Labels: []string{"b"}}
	c := sources.Task{Labels: []string{"c"}}
	ab := sources.Task{Labels: []string{"a", "b"}}

	cases := []struct {
		expr string
		task sources.Task
		want bool
	}{
		{"@a & @b", ab, true},
		{"@a & @b", a, false},
		{"@a | @b", b, true},
		{"@a | @b", c, false},
		{"!@a", b, true},
		{"!@a", a, false},
		// precedence: | is lowest, so @a | @b & @c parses as @a | (@b & @c).
		{"@a | @b & @c", a, true},  // left of | holds
		{"@a | @b & @c", b, false}, // @b alone does not satisfy @b & @c
		// grouping overrides precedence.
		{"(@a | @b) & @c", ab, false},
		{"(@a | @b) & @c", sources.Task{Labels: []string{"a", "c"}}, true},
	}
	for _, tc := range cases {
		pred, ok := compileFilter(tc.expr)
		if !ok {
			t.Errorf("compileFilter(%q) failed to parse", tc.expr)
			continue
		}
		if got := pred(tc.task); got != tc.want {
			t.Errorf("compileFilter(%q) on %+v = %v, want %v", tc.expr, tc.task.Labels, got, tc.want)
		}
	}
}

func TestCompileFilterNestingAndNegation(t *testing.T) {
	a := sources.Task{Labels: []string{"a"}}
	b := sources.Task{Labels: []string{"b"}}
	ab := sources.Task{Labels: []string{"a", "b"}}

	cases := []struct {
		expr string
		task sources.Task
		want bool
	}{
		{"((@a))", a, true}, // redundant nesting
		{"((@a))", b, false},
		{"!!@a", a, true}, // double negation is identity
		{"!!@a", b, false},
		{"!@a & @b", b, true},   // ! binds tighter than &: (!@a) & @b
		{"!@a & @b", ab, false}, // @a present, so !@a is false
		{"(@a | @b) & !@a", b, true},
		{"(@a | @b) & !@a", a, false},
	}
	for _, tc := range cases {
		pred, ok := compileFilter(tc.expr)
		if !ok {
			t.Errorf("compileFilter(%q) failed to parse", tc.expr)
			continue
		}
		if got := pred(tc.task); got != tc.want {
			t.Errorf("compileFilter(%q) on %v = %v, want %v", tc.expr, tc.task.Labels, got, tc.want)
		}
	}
}

func TestCompileFilterQuotedName(t *testing.T) {
	// A project or label whose name carries an operator character is expressible
	// only through quoting; unquoted it would split on the operator.
	pred, ok := compileFilter(`#"R&D"`)
	if !ok {
		t.Fatal(`compileFilter("#\"R&D\"") failed to parse`)
	}
	if !pred(sources.Task{Project: "R&D"}) {
		t.Error(`#"R&D" did not match project R&D`)
	}
	if pred(sources.Task{Project: "R"}) {
		t.Error(`#"R&D" matched project R; the & is literal inside quotes`)
	}
}

func TestTermPredicateBareSigilIsSubstring(t *testing.T) {
	// A lone @ or # (no name after it) is not a label/project term; it degrades to
	// a substring so it never matches everything or panics.
	for _, term := range []string{"@", "#"} {
		pred := termPredicate(term)
		if !pred(sources.Task{Title: "has " + term + " here"}) {
			t.Errorf("termPredicate(%q) should substring-match a title containing it", term)
		}
		if pred(sources.Task{Title: "nothing"}) {
			t.Errorf("termPredicate(%q) matched a title without it", term)
		}
	}
}

func TestCompileFilterSpaceAndCommaAdjacentTerms(t *testing.T) {
	// Space and comma are not operators, so adjacent terms fold into one term.
	// A label cannot contain a space or comma, so these match nothing rather than
	// AND/OR-ing; only & | ! compose. Pinned so the behavior is a decision, not a
	// surprise (the CLI reports the empty scope).
	for _, expr := range []string{"@a @b", "@a, @b"} {
		pred, ok := compileFilter(expr)
		if !ok {
			t.Errorf("compileFilter(%q) failed to parse", expr)
			continue
		}
		if pred(sources.Task{Labels: []string{"a", "b"}}) {
			t.Errorf("compileFilter(%q) matched labels [a b]; only & | ! compose terms", expr)
		}
	}
}

func TestCompileFilterDepthLimitDegrades(t *testing.T) {
	// Pathological nesting past the depth cap fails to parse (so the caller
	// degrades to substring) instead of growing the stack.
	deep := strings.Repeat("(", maxFilterDepth+5) + "@a" + strings.Repeat(")", maxFilterDepth+5)
	if _, ok := compileFilter(deep); ok {
		t.Error("deeply nested expression parsed, want ok=false past maxFilterDepth")
	}
}

func TestCompileFilterMalformed(t *testing.T) {
	for _, expr := range []string{"@a &", "(@a", "@a )", "", "   ", "&@a", "@a | | @b", "!", "()", "(@a | )"} {
		if _, ok := compileFilter(expr); ok {
			t.Errorf("compileFilter(%q) parsed, want ok=false", expr)
		}
	}
}
