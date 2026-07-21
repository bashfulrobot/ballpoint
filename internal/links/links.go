// Package links extracts external references from a task and parses each
// system's permalink into a stable record identity that keys a watermark.
package links

// System is the canonical name of an external system.
type System string

// The systems ballpoint recognises in a task's references.
const (
	SystemSlack      System = "slack"
	SystemTeams      System = "teams"
	SystemGmail      System = "gmail"
	SystemGDrive     System = "gdrive"
	SystemAha        System = "aha"
	SystemJira       System = "jira"
	SystemConfluence System = "confluence"
	SystemZoom       System = "zoom"
	SystemTodoist    System = "todoist"
	SystemSalesforce System = "salesforce"
	SystemGitHub     System = "github"
	SystemURL        System = "url" // an uncategorised URL
)

// Link is one reference from a task to an external record.
type Link struct {
	System System
	Raw    string            // the URL or bare id as it appeared
	Record string            // parsed record identity, empty when unparseable
	Fields map[string]string // parsed parts, e.g. channel, thread, fileID
}

// Key is the watermark key, "<system>:<record>", stable across runs.
func (l Link) Key() string { return string(l.System) + ":" + l.Record }
