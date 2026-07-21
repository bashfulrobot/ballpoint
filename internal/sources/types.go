package sources

import (
	"context"
	"time"
)

// Watermark records the last seen activity time per link key. A link key
// identifies one task's relationship to one external system; for Todoist it is
// "todoist:<taskID>".
type Watermark map[string]time.Time

// Comment is a normalised Todoist comment.
type Comment struct {
	ID         string
	Content    string
	PostedAt   time.Time
	Attachment string // file name, empty when none
}

// Task is the normalised shape every source returns, independent of any one
// API's field names.
type Task struct {
	ID          string
	Title       string
	Project     string // resolved name, not the raw id
	Section     string // resolved name, empty when none
	Due         string // date or natural language, empty when none
	Recurring   bool
	Priority    string // always p1 through p4, never the raw API integer
	Labels      []string
	Description string
	URL         string
	UpdatedAt   time.Time
	Comments    []Comment
}

// Delta is what a probe returns: the tasks it fetched, the link keys whose
// activity moved past the incoming watermark, and the watermark to persist.
type Delta struct {
	Tasks   []Task
	Changed []string
	Next    Watermark
}

// Source is one external system. Adding a system means adding one package that
// implements this and nothing else.
type Source interface {
	Name() string
	Probe(ctx context.Context, since Watermark) (Delta, error)
}
