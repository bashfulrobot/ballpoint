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

func TestParseDrive(t *testing.T) {
	rec, _ := parseDrive("https://docs.google.com/document/d/1AbC_dEF/edit")
	if rec != "1AbC_dEF" {
		t.Errorf("record = %q, want the file id 1AbC_dEF", rec)
	}
}
