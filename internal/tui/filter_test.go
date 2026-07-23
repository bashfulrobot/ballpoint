package tui

import (
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
	}
	for _, c := range cases {
		got := tokenizeFilter(c.in)
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

func TestCompileFilterMalformed(t *testing.T) {
	for _, expr := range []string{"@a &", "(@a", "@a )", "", "   ", "&@a", "@a | | @b", "!"} {
		if _, ok := compileFilter(expr); ok {
			t.Errorf("compileFilter(%q) parsed, want ok=false", expr)
		}
	}
}
