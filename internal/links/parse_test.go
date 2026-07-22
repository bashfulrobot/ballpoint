package links

import "testing"

func TestParseSlack(t *testing.T) {
	rec, fields := parseSlack("https://kong.slack.com/archives/C1/p1699999999000100")
	if rec != "C1:1699999999.000100" {
		t.Errorf("record = %q, want C1:1699999999.000100", rec)
	}
	if fields["channel"] != "C1" || fields["thread"] != "1699999999.000100" {
		t.Errorf("fields = %v", fields)
	}
}

// A reply permalink carries thread_ts, which is the parent thread, and it wins
// over the path ts so a reply and its parent share one watermark key.
func TestParseSlackThreadTSOverride(t *testing.T) {
	rec, _ := parseSlack("https://kong.slack.com/archives/C1/p1699999999000200?thread_ts=1699999999.000100")
	if rec != "C1:1699999999.000100" {
		t.Errorf("record = %q, want the thread_ts parent C1:1699999999.000100", rec)
	}
}

func TestParseSlackUnparseable(t *testing.T) {
	rec, _ := parseSlack("https://kong.slack.com/team/U123")
	if rec != "" {
		t.Errorf("record = %q, want empty for a non-archive slack url", rec)
	}
}

func TestParseGmail(t *testing.T) {
	rec, _ := parseGmail("https://mail.google.com/mail/u/0/#inbox/FMfcgzGabc123")
	if rec != "FMfcgzGabc123" {
		t.Errorf("record = %q, want the trailing thread id", rec)
	}
}

func TestParseAha(t *testing.T) {
	if rec, _ := parseAha("https://kong.aha.io/features/GTWY-I-1484"); rec != "GTWY-I-1484" {
		t.Errorf("url record = %q, want GTWY-I-1484", rec)
	}
	if rec, _ := parseAha("GTWY-I-1484"); rec != "GTWY-I-1484" {
		t.Errorf("bare record = %q, want GTWY-I-1484", rec)
	}
}

func TestParseSalesforceLightning(t *testing.T) {
	rec, f := parseSalesforce("https://myorg.lightning.force.com/lightning/r/Opportunity/006XX000004Ci1wYAC/view")
	if rec != "006XX000004Ci1wYAC" {
		t.Errorf("record = %q, want the 18-char id", rec)
	}
	if f["object"] != "Opportunity" {
		t.Errorf("object = %q, want Opportunity", f["object"])
	}
}

func TestParseSalesforceClassic(t *testing.T) {
	rec, f := parseSalesforce("https://na1.salesforce.com/006XX000004Ci1w")
	if rec != "006XX000004Ci1w" {
		t.Errorf("record = %q, want the 15-char id", rec)
	}
	if _, ok := f["object"]; ok {
		t.Errorf("classic url carries no object hint, got %v", f)
	}
}

func TestParseSalesforceClassicTrailingPath(t *testing.T) {
	rec, _ := parseSalesforce("https://na1.salesforce.com/006XX000004Ci1w/e")
	if rec != "006XX000004Ci1w" {
		t.Errorf("record = %q, want the id from the first path segment", rec)
	}
}

func TestParseSalesforceUnparseable(t *testing.T) {
	if rec, _ := parseSalesforce("https://myorg.lightning.force.com/lightning/o/Account/list"); rec != "" {
		t.Errorf("record = %q, want empty for a non-record salesforce url", rec)
	}
	// A 15-to-18 char run buried past the first path segment must not be minted
	// as a record, or the anchor is not doing its job.
	if rec, _ := parseSalesforce("https://na1.salesforce.com/setup/frontdoor001XX000003DHPh"); rec != "" {
		t.Errorf("record = %q, want empty for an id-like run past the path root", rec)
	}
}

func TestParseDrive(t *testing.T) {
	rec, _ := parseDrive("https://docs.google.com/document/d/1AbC_dEF/edit")
	if rec != "1AbC_dEF" {
		t.Errorf("record = %q, want the file id 1AbC_dEF", rec)
	}
}

func TestParseGitHubIssuePullCommit(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		wantRec  string
		wantKind string
		wantID   string
	}{
		{"issue", "https://github.com/bashfulrobot/ballpoint/issues/45", "bashfulrobot/ballpoint/issue/45", "issue", "45"},
		{"pull", "https://github.com/bashfulrobot/ballpoint/pull/12", "bashfulrobot/ballpoint/pull/12", "pull", "12"},
		{"pull files tab", "https://github.com/bashfulrobot/ballpoint/pull/12/files", "bashfulrobot/ballpoint/pull/12", "pull", "12"},
		{"commit", "https://github.com/bashfulrobot/ballpoint/commit/abcdef0123456789", "bashfulrobot/ballpoint/commit/abcdef0123456789", "commit", "abcdef0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, f := parseGitHub(tc.url)
			if rec != tc.wantRec {
				t.Errorf("record = %q, want %q", rec, tc.wantRec)
			}
			if f["kind"] != tc.wantKind {
				t.Errorf("kind = %q, want %q", f["kind"], tc.wantKind)
			}
			if f["id"] != tc.wantID {
				t.Errorf("id = %q, want %q", f["id"], tc.wantID)
			}
			if f["owner"] != "bashfulrobot" || f["repo"] != "ballpoint" {
				t.Errorf("owner/repo = %q/%q, want bashfulrobot/ballpoint", f["owner"], f["repo"])
			}
		})
	}
}

func TestParseGitHubUnparseable(t *testing.T) {
	for _, u := range []string{
		"https://github.com/bashfulrobot/ballpoint",                   // repo root
		"https://github.com/bashfulrobot/ballpoint/wiki/Home",         // wiki
		"https://github.com/bashfulrobot",                             // owner only
		"https://github.com/bashfulrobot/ballpoint/releases/tag/v1",   // release
		"https://github.com/bashfulrobot/ballpoint/issues/notanumber", // bad id
		"https://github.com/bashfulrobot/ballpoint/commit/xyz",        // non-hex sha
	} {
		if rec, _ := parseGitHub(u); rec != "" {
			t.Errorf("record = %q, want empty for %q", rec, u)
		}
	}
}
