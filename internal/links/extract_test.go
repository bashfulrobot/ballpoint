package links

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestExtractCategorises(t *testing.T) {
	task := sources.Task{
		ID:    "1",
		Title: "See https://kong.slack.com/archives/C1/p1699999999000100 and GTWY-I-1484",
		Comments: []sources.Comment{
			{Content: "doc https://docs.google.com/document/d/1AbC_dEF/edit."},
			{Content: "mail https://mail.google.com/mail/u/0/#inbox/FMfcgzGabc123"},
			{Content: "jira PLAT-42 and teams https://teams.microsoft.com/l/message/19:abc"},
		},
		UpdatedAt: time.Unix(1719000000, 0).UTC(),
	}

	got := Extract(task)

	systems := map[System]bool{}
	for _, l := range got {
		systems[l.System] = true
	}
	for _, want := range []System{SystemSlack, SystemAha, SystemGDrive, SystemGmail, SystemJira, SystemTeams} {
		if !systems[want] {
			t.Errorf("Extract missed system %q", want)
		}
	}

	rendered, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	golden.Assert(t, "extract.golden", string(rendered))
}

// The trailing period on the docs URL must be stripped before parsing, or the
// file id would carry it.
func TestExtractStripsTrailingPunct(t *testing.T) {
	task := sources.Task{ID: "2", Title: "x https://docs.google.com/document/d/FILEID/edit."}
	for _, l := range Extract(task) {
		if l.System == SystemGDrive && l.Record != "FILEID" {
			t.Errorf("drive record = %q, want FILEID with no trailing punctuation", l.Record)
		}
	}
}

// The same link appearing in the title and a comment yields one link, in
// first-seen order.
func TestExtractDedups(t *testing.T) {
	u := "https://kong.slack.com/archives/C1/p1699999999000100"
	task := sources.Task{ID: "3", Title: u, Comments: []sources.Comment{{Content: u}}}

	n := 0
	for _, l := range Extract(task) {
		if l.System == SystemSlack {
			n++
		}
	}
	if n != 1 {
		t.Errorf("slack link count = %d, want 1 (deduped)", n)
	}
}

// A jira bare key and an aha idea key are distinguished: aha keys carry -I- and
// must not be miscategorised as jira.
func TestExtractAhaNotJira(t *testing.T) {
	task := sources.Task{ID: "4", Title: "GTWY-I-1484 and PLAT-42"}
	sys := map[string]System{}
	for _, l := range Extract(task) {
		sys[l.Raw] = l.System
	}
	if sys["GTWY-I-1484"] != SystemAha {
		t.Errorf("GTWY-I-1484 = %q, want aha", sys["GTWY-I-1484"])
	}
	if sys["PLAT-42"] != SystemJira {
		t.Errorf("PLAT-42 = %q, want jira", sys["PLAT-42"])
	}
}
