package links

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	slackArchive = regexp.MustCompile(`/archives/([A-Z0-9]+)/p([0-9]+)`)
	gmailThread  = regexp.MustCompile(`#[^/]+/([A-Za-z0-9]+)$`)
	driveFile    = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)
	ahaKey       = regexp.MustCompile(`[A-Z]{3,5}-I-[0-9]+`)
)

// slackTS turns Slack's p-form (p1699999999000100) into a ts
// (1699999999.000100) by inserting the decimal six digits from the end.
func slackTS(p string) string {
	if len(p) <= 6 {
		return p
	}
	return p[:len(p)-6] + "." + p[len(p)-6:]
}

// parseSlack returns the record "<channel>:<ts>" for an archive permalink. A
// thread_ts query parameter (a reply permalink) overrides the path ts so a
// reply keys to its parent thread. A non-archive URL returns an empty record.
func parseSlack(raw string) (string, map[string]string) {
	m := slackArchive.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	channel, ts := m[1], slackTS(m[2])
	if u, err := url.Parse(raw); err == nil {
		if tt := u.Query().Get("thread_ts"); tt != "" {
			ts = tt
		}
	}
	return channel + ":" + ts, map[string]string{"channel": channel, "thread": ts}
}

// parseGmail returns the trailing thread id from a Gmail permalink.
func parseGmail(raw string) (string, map[string]string) {
	m := gmailThread.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	return m[1], map[string]string{"thread": m[1]}
}

// parseAha returns the Aha reference key from a URL or a bare key.
func parseAha(raw string) (string, map[string]string) {
	m := ahaKey.FindString(raw)
	if m == "" {
		return "", nil
	}
	return m, map[string]string{"reference": m}
}

// parseDrive returns the Drive file id from a docs or drive permalink.
func parseDrive(raw string) (string, map[string]string) {
	m := driveFile.FindStringSubmatch(raw)
	if m == nil {
		return "", nil
	}
	return m[1], map[string]string{"file": m[1]}
}

// stripTrailingPunct removes sentence punctuation a URL picked up from prose.
func stripTrailingPunct(s string) string {
	return strings.TrimRight(s, ".,;:!?")
}
