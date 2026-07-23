package tui

import (
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// filterPredicate reports whether a cached task satisfies a parsed filter.
type filterPredicate func(sources.Task) bool

// maxFilterDepth bounds parser recursion (nested parens and chained `!`), so a
// pathological expression degrades to the substring fallback instead of growing
// the stack. It is far above any hand-written filter.
const maxFilterDepth = 64

// compileFilter parses the Todoist filter subset the cache can answer offline
// (@label, #project, p1..p4, and & | ! with parentheses) into a predicate over
// cached tasks. ok is false for a malformed expression (unbalanced parens or
// quotes, a dangling or leading binary operator, an empty expression, or nesting
// past maxFilterDepth), so the caller can fall back to a substring match rather
// than dropping the walk. It never fetches and never panics.
func compileFilter(expr string) (filterPredicate, bool) {
	toks, ok := tokenizeFilter(expr)
	if !ok || len(toks) == 0 {
		return nil, false
	}
	p := &filterParser{toks: toks}
	pred, ok := p.parseOr()
	if !ok || !p.atEnd() {
		return nil, false
	}
	return pred, true
}

// substringMatch reports whether the lowercased query q appears in the task's
// title, any label, or its section. It is the offline fallback both for a filter
// term the parser treats as free text and for a whole expression that fails to
// parse.
func substringMatch(t sources.Task, q string) bool {
	if strings.Contains(strings.ToLower(t.Title), q) {
		return true
	}
	for _, l := range t.Labels {
		if strings.Contains(strings.ToLower(l), q) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(t.Section), q)
}

// filterTokenKind is the lexical class of a filter token.
type filterTokenKind int

const (
	tokTerm   filterTokenKind = iota // a bare term: @label, #project, pN, or free text
	tokAnd                           // &
	tokOr                            // |
	tokNot                           // !
	tokLParen                        // (
	tokRParen                        // )
)

// filterToken is one lexical unit. text is set only for tokTerm.
type filterToken struct {
	kind filterTokenKind
	text string
}

// tokenizeFilter splits an expression on the operators & | ! ( ), treating every
// other run of characters (spaces included) as a single term. Splitting only on
// operators means multi-word project or label names keep their internal spaces;
// each term is trimmed and an empty term is dropped. A double-quoted span is a
// literal: operators and spaces inside it join the current term, so a name that
// contains an operator character can be written as #"R&D" or @"in progress". ok
// is false for an unterminated quote, which lets the caller degrade the whole
// expression to a substring rather than guessing.
func tokenizeFilter(expr string) ([]filterToken, bool) {
	var toks []filterToken
	var buf strings.Builder
	flush := func() {
		term := strings.TrimSpace(buf.String())
		buf.Reset()
		if term != "" {
			toks = append(toks, filterToken{kind: tokTerm, text: term})
		}
	}
	runes := []rune(expr)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '"':
			// Consume the literal span up to the closing quote; its contents (any
			// operator or space) join the current term buffer verbatim.
			i++
			for i < len(runes) && runes[i] != '"' {
				buf.WriteRune(runes[i])
				i++
			}
			if i >= len(runes) {
				return nil, false // unterminated quote
			}
		case '&':
			flush()
			toks = append(toks, filterToken{kind: tokAnd})
		case '|':
			flush()
			toks = append(toks, filterToken{kind: tokOr})
		case '!':
			flush()
			toks = append(toks, filterToken{kind: tokNot})
		case '(':
			flush()
			toks = append(toks, filterToken{kind: tokLParen})
		case ')':
			flush()
			toks = append(toks, filterToken{kind: tokRParen})
		default:
			buf.WriteRune(runes[i])
		}
	}
	flush()
	return toks, true
}

// termPredicate compiles a single term into a predicate. @label and #project are
// case-insensitive equality; pN (N in 1..4) matches the task priority; anything
// else degrades to a substring match over title, labels, and section.
func termPredicate(term string) filterPredicate {
	switch {
	case strings.HasPrefix(term, "@") && len(term) > 1:
		label := term[1:]
		return func(t sources.Task) bool {
			for _, l := range t.Labels {
				if strings.EqualFold(l, label) {
					return true
				}
			}
			return false
		}
	case strings.HasPrefix(term, "#") && len(term) > 1:
		project := term[1:]
		return func(t sources.Task) bool { return strings.EqualFold(t.Project, project) }
	case isPriorityTerm(term):
		want := strings.ToLower(term)
		return func(t sources.Task) bool { return strings.EqualFold(t.Priority, want) }
	default:
		q := strings.ToLower(term)
		return func(t sources.Task) bool { return substringMatch(t, q) }
	}
}

// isPriorityTerm reports whether term is p1..p4 (case-insensitive), the priority
// filter the cache carries as Task.Priority.
func isPriorityTerm(term string) bool {
	if len(term) != 2 {
		return false
	}
	if term[0] != 'p' && term[0] != 'P' {
		return false
	}
	return term[1] >= '1' && term[1] <= '4'
}

// filterParser is a recursive-descent parser over the token stream. Precedence,
// tightest first: ! then & then |. Parentheses group.
type filterParser struct {
	toks  []filterToken
	pos   int
	depth int // current nesting, bounded by maxFilterDepth
}

func (p *filterParser) atEnd() bool { return p.pos >= len(p.toks) }

func (p *filterParser) peek() (filterToken, bool) {
	if p.atEnd() {
		return filterToken{}, false
	}
	return p.toks[p.pos], true
}

// parseOr := parseAnd ( '|' parseAnd )*
func (p *filterParser) parseOr() (filterPredicate, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxFilterDepth {
		return nil, false
	}
	left, ok := p.parseAnd()
	if !ok {
		return nil, false
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokOr {
			break
		}
		p.pos++
		right, ok := p.parseAnd()
		if !ok {
			return nil, false
		}
		l, r := left, right
		left = func(task sources.Task) bool { return l(task) || r(task) }
	}
	return left, true
}

// parseAnd := parseNot ( '&' parseNot )*
func (p *filterParser) parseAnd() (filterPredicate, bool) {
	left, ok := p.parseNot()
	if !ok {
		return nil, false
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tokAnd {
			break
		}
		p.pos++
		right, ok := p.parseNot()
		if !ok {
			return nil, false
		}
		l, r := left, right
		left = func(task sources.Task) bool { return l(task) && r(task) }
	}
	return left, true
}

// parseNot := '!' parseNot | parsePrimary
func (p *filterParser) parseNot() (filterPredicate, bool) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxFilterDepth {
		return nil, false
	}
	t, ok := p.peek()
	if !ok {
		return nil, false
	}
	if t.kind == tokNot {
		p.pos++
		inner, ok := p.parseNot()
		if !ok {
			return nil, false
		}
		return func(task sources.Task) bool { return !inner(task) }, true
	}
	return p.parsePrimary()
}

// parsePrimary := '(' parseOr ')' | TERM
func (p *filterParser) parsePrimary() (filterPredicate, bool) {
	t, ok := p.peek()
	if !ok {
		return nil, false
	}
	switch t.kind {
	case tokLParen:
		p.pos++
		inner, ok := p.parseOr()
		if !ok {
			return nil, false
		}
		closing, ok := p.peek()
		if !ok || closing.kind != tokRParen {
			return nil, false
		}
		p.pos++
		return inner, true
	case tokTerm:
		p.pos++
		return termPredicate(t.text), true
	default:
		// An operator or a stray ) where a primary was expected.
		return nil, false
	}
}
