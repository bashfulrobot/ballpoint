//go:build tools

// Package tools exists only to hold dependency declarations. Issue #1 fixes
// the library choices for the TUI (#5), the probe engine (#3), and the
// Todoist source (#2) so those issues do not have to revisit them.
//
// Go drops modules nothing imports, so a go.mod entry alone would not survive
// `go mod tidy`. These blank imports hold the pins. `go mod tidy` matches all
// build configurations, so the tools tag does not hide them from it, while
// keeping every one of these packages out of the shipped binary.
//
// The pins live in their own package rather than inside internal/tui because
// errgroup and the rate limiter belong to the probe engine, not the TUI. That
// keeps #5 from inheriting a build-tagged file full of #3's dependencies.
//
// Do not delete this file because nothing appears to use it. Deleting it
// drops the pins on the next `go mod tidy`. CI compiles it with
// `go build -tags tools ./...` and lints it with the same tag, so a broken
// import here fails rather than sitting undetected.
package tools

import (
	// Blank imports on purpose. Nothing calls these yet; they exist so
	// `go mod tidy` keeps the versions pinned for issues #2, #3, and #5.
	_ "github.com/charmbracelet/bubbles/list"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/glamour"
	_ "github.com/charmbracelet/huh"
	_ "github.com/charmbracelet/lipgloss"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/time/rate"
)
