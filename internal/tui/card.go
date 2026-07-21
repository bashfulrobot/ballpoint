package tui

import (
	"sort"
	"strconv"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// LinkState is how a link's freshness renders in the footer.
type LinkState int

// LinkState values, one per freshness outcome.
const (
	LinkFresh     LinkState = iota // checked, no new activity since the work-log
	LinkMoved                      // external activity newer than the work-log
	LinkUnchecked                  // could not be verified this run
)

// LinkLine is one rendered freshness row.
type LinkLine struct {
	System string
	State  LinkState
	Detail string // "moved", an age string, or the unchecked reason
}

// Card is the pure view-model for one task. Rendering turns it into styled text;
// this holds no styling, so it is fully unit-testable.
type Card struct {
	TaskID string
	Task   sources.Task
	Report probe.TaskReport
	Moved  bool // any link moved; drives the sort-first ordering
	Links  []LinkLine
}

// BuildCard derives the view-model from a task and its freshness report. now is
// injected so the age strings are deterministic under test.
func BuildCard(task sources.Task, rep probe.TaskReport, now time.Time) Card {
	c := Card{TaskID: task.ID, Task: task, Report: rep}
	for _, l := range rep.Links {
		line := LinkLine{System: l.System}
		switch {
		case l.Unchecked:
			line.State = LinkUnchecked
			line.Detail = string(l.Reason)
		case l.Changed:
			line.State = LinkMoved
			line.Detail = "moved"
			c.Moved = true
		default:
			line.State = LinkFresh
			if l.LastActivity != nil {
				line.Detail = humanizeAge(now.Sub(*l.LastActivity))
			}
		}
		c.Links = append(c.Links, line)
	}
	return c
}

// SortMovedFirst is a stable partition: cards whose links moved come first,
// original order kept within each group, so nothing is hidden and the rest stays
// in scope order.
func SortMovedFirst(cards []Card) {
	sort.SliceStable(cards, func(i, j int) bool {
		return cards[i].Moved && !cards[j].Moved
	})
}

// humanizeAge renders a coarse age like "2d", "3h", or "12m". A zero or negative
// duration renders "0m" rather than a negative number.
func humanizeAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	}
}
