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

// An Aha key inside an aha.io URL is captured once, from the URL, and not again
// as a bare id. The URL and the bare-id scan would otherwise both claim it.
func TestExtractNoDoubleCountInsideURL(t *testing.T) {
	task := sources.Task{ID: "5", Title: "see https://company.aha.io/features/DEVP-I-123 please"}

	n := 0
	for _, l := range Extract(task) {
		if l.System == SystemAha && l.Record == "DEVP-I-123" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("aha:DEVP-I-123 appeared %d times, want 1 (no bare double-count inside the URL)", n)
	}
}

// A Lightning record URL is categorised as Salesforce, carries the object hint,
// and is counted once, not a second time as a bare id inside the URL.
func TestExtractSalesforceLightningURL(t *testing.T) {
	u := "https://myorg.lightning.force.com/lightning/r/Account/001XX000003DHPhYAO/view"
	task := sources.Task{ID: "sf1", Title: "acct " + u}

	var sf []Link
	for _, l := range Extract(task) {
		if l.System == SystemSalesforce {
			sf = append(sf, l)
		}
	}
	if len(sf) != 1 {
		t.Fatalf("salesforce link count = %d, want 1 (no bare double-count inside the URL)", len(sf))
	}
	if sf[0].Record != "001XX000003DHPhYAO" || sf[0].Fields["object"] != "Account" {
		t.Errorf("link = %+v, want record 001XX000003DHPhYAO object Account", sf[0])
	}
}

// A bare Case-prefixed id (500...) is recognised as Salesforce; the old regex
// only matched 00-prefixed ids.
func TestExtractSalesforceBareCaseID(t *testing.T) {
	task := sources.Task{ID: "sf2", Title: "record 500XX000001AbcdEAG here"}
	found := false
	for _, l := range Extract(task) {
		if l.System == SystemSalesforce && l.Record == "500XX000001AbcdEAG" {
			found = true
		}
	}
	if !found {
		t.Error("bare 500-prefixed id was not extracted as salesforce")
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
