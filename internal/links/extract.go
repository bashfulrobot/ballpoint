package links

import (
	"regexp"
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

var (
	urlRe  = regexp.MustCompile(`https?://[^\s)<>"']+`)
	jiraRe = regexp.MustCompile(`\b[A-Z]{2,6}-[0-9]+\b`)
	sfidRe = regexp.MustCompile(`\b00[0-9A-Za-z]{13}([0-9A-Za-z]{3})?\b`)
	caseRe = regexp.MustCompile(`\bCase [0-9]{5,}\b`)
)

// Extract scans a task's title and comment bodies, harvests URLs and bare
// identifiers, categorises each, parses a record identity where it can, and
// returns the deduplicated links in first-seen order.
func Extract(task sources.Task) []Link {
	var blob strings.Builder
	blob.WriteString(task.Title)
	blob.WriteByte('\n')
	for _, c := range task.Comments {
		blob.WriteString(c.Content)
		blob.WriteByte('\n')
	}
	text := blob.String()

	seen := map[string]bool{}
	var out []Link

	add := func(l Link) {
		k := string(l.System) + "|" + l.Raw
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, l)
	}

	for _, raw := range urlRe.FindAllString(text, -1) {
		raw = stripTrailingPunct(raw)
		add(categoriseURL(raw))
	}

	// Bare identifiers are scanned over the text with URLs blanked out, so an id
	// that already appears inside a harvested URL (an Aha key in an aha.io link,
	// a record id in a permalink) is not counted a second time as a bare match.
	bare := urlRe.ReplaceAllString(text, " ")

	// Aha keys (with -I-) are matched before Jira so the Jira pattern does not
	// claim them.
	for _, m := range ahaKey.FindAllString(bare, -1) {
		rec, f := parseAha(m)
		add(Link{System: SystemAha, Raw: m, Record: rec, Fields: f})
	}
	for _, m := range jiraRe.FindAllString(bare, -1) {
		if strings.Contains(m, "-I-") {
			continue
		}
		add(Link{System: SystemJira, Raw: m, Record: m})
	}
	for _, m := range sfidRe.FindAllString(bare, -1) {
		add(Link{System: SystemSalesforce, Raw: m, Record: m})
	}
	for _, m := range caseRe.FindAllString(bare, -1) {
		add(Link{System: SystemSalesforce, Raw: m, Record: strings.TrimPrefix(m, "Case ")})
	}

	return out
}

// categoriseURL maps a URL to its system by host substring and parses a record
// where the system has a parser.
func categoriseURL(raw string) Link {
	host := strings.ToLower(raw)
	switch {
	case strings.Contains(host, "slack.com"):
		rec, f := parseSlack(raw)
		return Link{System: SystemSlack, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "teams.microsoft.com"):
		return Link{System: SystemTeams, Raw: raw}
	case strings.Contains(host, "mail.google.com"):
		rec, f := parseGmail(raw)
		return Link{System: SystemGmail, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "docs.google.com"), strings.Contains(host, "drive.google.com"):
		rec, f := parseDrive(raw)
		return Link{System: SystemGDrive, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, ".aha.io"):
		rec, f := parseAha(raw)
		return Link{System: SystemAha, Raw: raw, Record: rec, Fields: f}
	case strings.Contains(host, "atlassian.net"):
		return Link{System: SystemJira, Raw: raw}
	case strings.Contains(host, "confluence"):
		return Link{System: SystemConfluence, Raw: raw}
	case strings.Contains(host, "github.com"):
		return Link{System: SystemGitHub, Raw: raw}
	case strings.Contains(host, "zoom.us"):
		return Link{System: SystemZoom, Raw: raw}
	case strings.Contains(host, "app.todoist.com"):
		return Link{System: SystemTodoist, Raw: raw}
	default:
		return Link{System: SystemURL, Raw: raw}
	}
}
