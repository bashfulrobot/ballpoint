# GitHub freshness prober implementation plan

**Goal:** Probe GitHub issue/PR/commit freshness through the authenticated `gh` CLI, so GitHub links stop rendering `unchecked`.

**Architecture:** CLI-owned auth, a direct port of `internal/probe/salesforce`. A URL parser gives each `github.com` link a stable record; the prober shells one `gh api` call per link through an injectable `Runner`, maps the timestamp to a `probe.Result`, and renders every failure `unchecked`.

**Tech stack:** Go, `golang.org/x/time/rate`, `gh` CLI, `os/exec`.

## Task 1: parse GitHub URLs (`internal/links/parse.go`, `parse_test.go`)

- Add `ghURLRe` and charset guards. `parseGitHub(raw)` extracts owner, repo, kind, id from `/<owner>/<repo>/(issues|pull|commit)/<id>`.
- Normalize kind to `issue|pull|commit`. Validate owner `^[A-Za-z0-9](?:[A-Za-z0-9-]*)$`, repo `^[A-Za-z0-9._-]+$`, number `^[0-9]+$`, sha `^[0-9a-fA-F]{7,40}$`.
- Record = `owner/repo/kind/id`. Fields = `{owner, repo, kind, id}`. Non-matching URL returns `("", nil)`.
- TDD: table test over issue/pull/commit URLs, a repo-root URL, a wiki URL, and a trailing-slash URL.

## Task 2: wire parser into `categoriseURL` (`internal/links/extract.go`, `extract_test.go`)

- Replace the bare `Link{System: SystemGitHub, Raw: raw}` at the `github.com` case with `parseGitHub`-derived record and fields.
- TDD: a task with a PR URL yields a link whose Record is the canonical path; a repo-root URL yields an empty record.

## Task 3: the prober (`internal/probe/github/github.go`, `github_test.go`)

- `Client{runner, limiter}`, `Runner`, `WithRunner`, `New`, `Available()` (`exec.LookPath("gh")`), `System()`.
- `Probe`: validate each link's fields (defense in depth, records come from an on-disk cache), sort valid links by key, cap at `maxProbes`, one `gh api repos/<o>/<r>/<endpoint>/<id>` per link under the limiter.
- Endpoint map: issue→issues, pull→pulls, commit→commits. Timestamp: `updated_at` for issue/pull, `.commit.committer.date` for commit.
- Classify: run error with an auth marker → `ReasonAuth`; other run error → `ReasonError` (context deadline → `ReasonFromCtx`); missing/unparseable timestamp → `ReasonError`; empty record → `ReasonUnparseable`; over cap → `ReasonTooMany`.
- TDD: happy path per kind (asserting the exact `gh api` argv), auth-vs-error classification, injection guard (a tampered record never reaches the runner), the cap, a missing timestamp.

## Task 4: registration + wiring (`probeset.go`, `probeset_test.go`, `cli/probe.go`)

- `Credentials.GitHub bool`; register `github.New()` when true.
- `deps.creds.GitHub = github.Available()` beside the salesforce gate.

## Task 5: docs + verify

- README: GitHub row (auth from `gh`, no secret).
- `git add` new files, then `nix develop --command go test ./...`, `golangci-lint run ./...`, `nix build`.
