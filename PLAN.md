# Todoist filter parser for the walk scope — Implementation Plan

> **For agentic workers:** TDD, bite-sized steps, frequent commits. Steps use checkbox syntax.

**Goal:** Replace the substring-only `--filter`/`--preset` degrade in `internal/tui/scope.go` with a small offline parser for the Todoist filter subset the cache can answer (`@label`, `#project`, `p1..p4`, `& | !`, parens), falling back to substring for anything unrecognized or malformed.

**Architecture:** A new `internal/tui/filter.go` holds a tokenizer plus a recursive-descent parser that compiles an expression to a `filterPredicate func(sources.Task) bool`. `scope.go` compiles the predicate once per `Resolve` (not per task) and applies it; an expression that fails to parse degrades to the existing substring match. No new dependency, no network.

**Tech Stack:** Go 1.26, `strings`, table-driven tests. `nix develop --command go test ./...`, `golangci-lint run ./...`.

---

## File structure

- Create: `internal/tui/filter.go` — tokenizer, parser, `termPredicate`, `substringMatch`, `compileFilter`.
- Modify: `internal/tui/scope.go` — refactor `matches` into a compiled `predicate()`; wire `compileFilter` into `ScopeFilter`/`ScopePreset` with substring fallback.
- Create: `internal/tui/filter_test.go` — table tests over the grammar, precedence, malformed input, fallback.

## Grammar and semantics

- `@label` → case-insensitive equality against any entry in `Task.Labels`.
- `#project` → case-insensitive equality against `Task.Project` (resolved name).
- `pN` (N in 1..4, case-insensitive) → equality against `Task.Priority` (already `"p1".."p4"`).
- Any other bare term → substring over lowercased title, labels, section (the current fallback, extracted to `substringMatch`).
- Composition: `!` binds tightest, then `&`, then `|`. Parentheses group. Tokenizer splits only on `& | ! ( )`, so multi-word names (`#My Project`) and spaces stay inside a term (trimmed).
- Malformed (unbalanced parens, dangling/leading binary operator, empty) → `compileFilter` returns `ok=false`; caller uses substring fallback over the raw value. Never panics.

## Grammar (recursive descent)

```
or   := and ( '|' and )*
and  := not ( '&' not )*
not  := '!' not | primary
primary := '(' or ')' | TERM
```

---

### Task 1: substringMatch helper + tokenizer

**Files:**
- Create: `internal/tui/filter.go`
- Test: `internal/tui/filter_test.go`

- [ ] **Step 1: Write failing tests** for `substringMatch` (title/label/section hit + miss) and `tokenizeFilter` (`@waiting & #Work` → term,and,term; parens and `!` split; whitespace trimmed; multi-word term kept).
- [ ] **Step 2: Run — expect FAIL** (undefined `substringMatch`/`tokenizeFilter`).
- [ ] **Step 3: Implement** `filterPredicate` type, `substringMatch(t, q)`, token kinds, `filterToken`, `tokenizeFilter` (accumulate into a buffer, flush trimmed term on each operator/paren).
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

### Task 2: termPredicate

**Files:** Modify `internal/tui/filter.go`, `internal/tui/filter_test.go`.

- [ ] **Step 1: Write failing tests** for `termPredicate`: `@Waiting` matches a task labeled `waiting` (case-insensitive) and not one without; `#work` matches project `Work` exactly, not a substring; `p1` matches Priority `p1` only; `overdue` degrades to substring.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** `termPredicate(term)` with the `@`/`#`/`pN`/substring dispatch and `isPriorityTerm`.
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

### Task 3: parser + compileFilter

**Files:** Modify `internal/tui/filter.go`, `internal/tui/filter_test.go`.

- [ ] **Step 1: Write failing tests** for `compileFilter`: `@a & @b`, `@a | @b`, `!@a`, precedence `@a | @b & @c` == `@a | (@b & @c)`, grouping `(@a | @b) & #P`; malformed `@a &`, `(@a`, `@a )`, ``, `&@a` → `ok=false`.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** `filterParser` (parseOr/parseAnd/parseNot/parsePrimary, capturing left/right into closures) and `compileFilter` (tokenize, parse, require `atEnd()`).
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

### Task 4: wire into scope.go

**Files:** Modify `internal/tui/scope.go`, `internal/tui/scope_test.go`.

- [ ] **Step 1: Write failing test** at the `Resolve` level: `@waiting & #Work` returns only the waiting-labelled task in project Work; `p1 | overdue` parses and returns the p1 task plus any substring hit on "overdue"; a malformed filter still returns substring matches (no panic). Keep the existing `TestScopeFilterSubstring`.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Refactor** `matches` into `predicate()` compiled once in `Resolve`; `ScopeFilter`/`ScopePreset` use `compileFilter` then substring fallback. Remove the now-dead `matches`.
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

### Task 5: verify

- [ ] `nix develop --command go test ./...` green.
- [ ] `nix develop --command golangci-lint run ./...` clean.
- [ ] `nix build` and `nix flake check` pass.
