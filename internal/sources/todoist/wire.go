package todoist

import (
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

// parseTime parses an RFC3339 timestamp, returning the zero time on empty or
// malformed input rather than an error, so one odd field never fails a fetch.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// toTask converts a raw task into the normalised shape, resolving project and
// section ids to names and mapping the inverted priority. An unknown id
// resolves to the raw id so a card is never mislabelled as having none. The
// watermark time is updated_at, falling back to added_at.
func (r rawTask) toTask(projects, sections map[string]string) sources.Task {
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

	updated := parseTime(r.UpdatedAt)
	if updated.IsZero() {
		updated = parseTime(r.AddedAt)
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
	}
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
		PostedAt:   parseTime(r.PostedAt),
		Attachment: attachment,
	}
}
