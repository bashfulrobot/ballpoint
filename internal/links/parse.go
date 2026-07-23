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
	sfLightning  = regexp.MustCompile(`/lightning/r/([A-Za-z][A-Za-z0-9_]*)/([0-9A-Za-z]{15,18})`)
	// sfRecordID anchors the id to the first path segment after the host, the
	// shape of a classic record URL (instance.salesforce.com/<Id>). Anchoring
	// keeps it from latching onto a 15-to-18 char run buried elsewhere in the
	// URL (a frontdoor token, a session parameter), which would mint a bogus
	// record and cost a wasted query.
	sfRecordID = regexp.MustCompile(`^https?://[^/]+/([0-9A-Za-z]{15,18})(?:[/?#]|$)`)
	// GitHub record-part charsets. owner is a login (no dots or underscores),
	// repo also admits dot and underscore, a number is the issue or PR id, and a
	// sha is a 7-to-40 char hex commit id.
	ghOwner  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*)$`)
	ghRepo   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	ghNumber = regexp.MustCompile(`^[0-9]+$`)
	ghSha    = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
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

// parseSalesforce returns the record id from a Salesforce permalink. A Lightning
// record URL (/lightning/r/<Object>/<Id>/) also yields the object name, which the
// prober prefers over the id key prefix. A classic record URL (/<Id>) yields the
// id alone. A URL that is not a record permalink returns an empty record.
func parseSalesforce(raw string) (string, map[string]string) {
	if m := sfLightning.FindStringSubmatch(raw); m != nil {
		return m[2], map[string]string{"object": m[1], "record": m[2]}
	}
	if m := sfRecordID.FindStringSubmatch(raw); m != nil {
		return m[1], map[string]string{"record": m[1]}
	}
	return "", nil
}

// parseGitHub returns the record "<owner>/<repo>/<kind>/<id>" for an issue, pull
// request, or commit URL, where kind is normalized to issue, pull, or commit.
// Any other GitHub URL (a repo root, a wiki page, a raw file, a release) returns
// an empty record. Only github.com is handled; an enterprise host is not probed
// here, since the gh CLI targets api.github.com. A pull URL with a deeper path
// (/pull/12/commits/<sha>) resolves to the pull request itself, not the commit.
// Every part is charset validated here so a record that reaches the prober is
// safe to splice into a gh api path.
func parseGitHub(raw string) (string, map[string]string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", nil
	}
	if host := strings.ToLower(u.Hostname()); host != "github.com" && host != "www.github.com" {
		return "", nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 {
		return "", nil
	}
	// A login and a repo name are case-insensitive on GitHub, so lowercase them
	// for a stable record: /Owner/Repo and /owner/repo dedup to one probe. The id
	// keeps its casing (a commit sha is matched as given).
	owner, repo, seg, id := strings.ToLower(parts[0]), strings.ToLower(parts[1]), parts[2], parts[3]
	var kind string
	switch seg {
	case "issues":
		kind = "issue"
	case "pull":
		kind = "pull"
	case "commit":
		kind = "commit"
	default:
		return "", nil
	}
	if !ghOwner.MatchString(owner) || !ghRepo.MatchString(repo) || isDotSegment(repo) {
		return "", nil
	}
	if kind == "commit" {
		if !ghSha.MatchString(id) {
			return "", nil
		}
	} else if !ghNumber.MatchString(id) {
		return "", nil
	}
	rec := owner + "/" + repo + "/" + kind + "/" + id
	return rec, map[string]string{"owner": owner, "repo": repo, "kind": kind, "id": id}
}

// isDotSegment reports whether a path segment is "." or "..", which the repo
// charset admits (dot is legal in a repo name like .github) but which would
// build a path-traversal segment such as repos/o/../issues/5. A real repo is
// never exactly "." or "..".
func isDotSegment(s string) bool { return s == "." || s == ".." }

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
