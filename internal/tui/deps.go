//go:build tools

// This file exists only to hold dependency declarations. Issue #1 fixes the
// library choices for the TUI (#5), the probe engine (#3), and the Todoist
// source (#2) so those issues do not have to revisit them.
//
// Go drops modules nothing imports, so a go.mod entry alone would not survive
// `go mod tidy`. These blank imports hold the pins. `go mod tidy` matches all
// build configurations, so the tools tag does not hide them from it, while
// keeping every one of these packages out of the shipped binary.
//
// Do not delete this file because nothing appears to use it. Deleting it drops
// the pins on the next `go mod tidy`.
package tui

import (
	_ "github.com/charmbracelet/bubbles/list"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/glamour"
	_ "github.com/charmbracelet/huh"
	_ "github.com/charmbracelet/lipgloss"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/time/rate"
)
