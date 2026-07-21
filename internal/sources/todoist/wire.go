package todoist

import (
	"fmt"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// rawDue is Todoist v1's due object.
type rawDue struct {
	Date        string `json:"date"`
	String      string `json:"string"`
	IsRecurring bool   `json:"is_recurring"`
}

// rawTask is the subset of Todoist v1's task object ballpoint decodes. Every
// field name the client depends on lives here, so a Todoist rename is a
// one-place fix. Field names are v1 snake_case.
type rawTask struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	ProjectID string   `json:"project_id"`
	SectionID string   `json:"section_id"`
	Priority  int      `json:"priority"`
	Labels    []string `json:"labels"`
	Desc      string   `json:"description"`
	URL       string   `json:"url"`
	AddedAt   string   `json:"added_at"`
	UpdatedAt string   `json:"updated_at"`
	Due       *rawDue  `json:"due"`
}

// rawComment is the subset of a Todoist v1 comment ballpoint decodes.
type rawComment struct {
	ID         string         `json:"id"`
	Content    string         `json:"content"`
	PostedAt   string         `json:"posted_at"`
	Attachment *rawAttachment `json:"file_attachment"`
}

type rawAttachment struct {
	FileName string `json:"file_name"`
}

// rawNamed is the shape shared by projects and sections: an id and a name.
type rawNamed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// parseTimeLenient parses an RFC3339 timestamp for a non-critical field,
// returning the zero time on empty or malformed input. Used for a comment's
// posted_at, where one odd value should not fail the fetch and nothing keys a
// watermark off it.
func parseTimeLenient(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// watermarkTime resolves a task's activity time: updated_at when present,
// otherwise added_at. A field that is present but does not parse is an error
// rather than a silent zero, because a zero watermark disables change
// detection permanently. If Todoist ever drifts its timestamp format the probe
// then fails loudly instead of going quiet. A task carrying neither field is
// not an error; it yields the zero time and always reads as changed.
func watermarkTime(updatedAt, addedAt string) (time.Time, error) {
	if updatedAt != "" {
		t, err := time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err)
		}
		return t, nil
	}
	if addedAt != "" {
		t, err := time.Parse(time.RFC3339, addedAt)
		if err != nil {
			return time.Time{}, fmt.Errorf("parsing added_at %q: %w", addedAt, err)
		}
		return t, nil
	}
	return time.Time{}, nil
}

// toTask converts a raw task into the normalised shape, resolving project and
// section ids to names and mapping the inverted priority. An unknown id
// resolves to the raw id so a card is never mislabelled as having none. It
// returns an error when a present timestamp fails to parse, so a format drift
// surfaces rather than silently zeroing the watermark.
//
// The Title, Description, and Labels are user-authored text carried through
// unchanged. In a shared project a collaborator controls them, so any later
// consumer that renders this into a model prompt (the triage TUI in #5) must
// treat it as untrusted and fence it there.
func (r rawTask) toTask(projects, sections map[string]string) (sources.Task, error) {
	project := r.ProjectID
	if name, ok := projects[r.ProjectID]; ok {
		project = name
	}

	section := ""
	if r.SectionID != "" {
		section = r.SectionID
		if name, ok := sections[r.SectionID]; ok {
			section = name
		}
	}

	updated, err := watermarkTime(r.UpdatedAt, r.AddedAt)
	if err != nil {
		return sources.Task{}, fmt.Errorf("task %s: %w", r.ID, err)
	}

	due, recurring := "", false
	if r.Due != nil {
		due = r.Due.Date
		if due == "" {
			due = r.Due.String
		}
		recurring = r.Due.IsRecurring
	}

	return sources.Task{
		ID:          r.ID,
		Title:       r.Content,
		Project:     project,
		Section:     section,
		Due:         due,
		Recurring:   recurring,
		Priority:    sources.NormalizePriority(r.Priority),
		Labels:      r.Labels,
		Description: r.Desc,
		URL:         r.URL,
		UpdatedAt:   updated,
	}, nil
}

// toComment converts a raw comment into the normalised shape.
func (r rawComment) toComment() sources.Comment {
	attachment := ""
	if r.Attachment != nil {
		attachment = r.Attachment.FileName
	}
	return sources.Comment{
		ID:         r.ID,
		Content:    r.Content,
		PostedAt:   parseTimeLenient(r.PostedAt),
		Attachment: attachment,
	}
}
