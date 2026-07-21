package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func tp(s string) *time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &v
}

// A prober that errors makes every link for its system unchecked, and no
// watermark is written for those links.
func TestRunUncheckedOnError(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-07-01T00:00:00Z"),
	}}

	var reg Registry
	reg.Register(stubProber{system: links.SystemSlack, err: errors.New("boom")})

	report, next, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	link := report.Tasks["1"].Links[0]
	if !link.Unchecked || link.Reason != ReasonError {
		t.Errorf("link = %+v, want unchecked with ReasonError", link)
	}
	if _, ok := next[link.Key]; ok {
		t.Error("a watermark was written for an unchecked link, want none")
	}
}

// A system with no registered prober is unchecked with ReasonNoProbe; Teams is
// unchecked with ReasonNotProbeable.
func TestRunUncheckedNoProbe(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "gh https://github.com/o/r/pull/1 teams https://teams.microsoft.com/l/message/19:a",
		UpdatedAt: *tp("2026-07-01T00:00:00Z"),
	}}

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &Registry{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	byReason := map[Reason]bool{}
	for _, l := range report.Tasks["1"].Links {
		if l.Unchecked {
			byReason[l.Reason] = true
		}
	}
	if !byReason[ReasonNoProbe] {
		t.Error("github link not unchecked with ReasonNoProbe")
	}
	if !byReason[ReasonNotProbeable] {
		t.Error("teams link not unchecked with ReasonNotProbeable")
	}
}

// Changed is true when the last activity is after the newest comment, and the
// watermark advances only for the checked link.
func TestRunChangedAndWatermark(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-06-01T00:00:00Z"),
		Comments:  []sources.Comment{{PostedAt: *tp("2026-07-01T00:00:00Z")}},
	}}

	activity := tp("2026-07-10T00:00:00Z")
	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]Result{"slack:C1:1699999999.000100": {LastActivity: activity}},
	})

	report, next, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	link := report.Tasks["1"].Links[0]
	if !link.Changed {
		t.Error("changed = false, want true (activity after the newest comment)")
	}
	if !next["slack:C1:1699999999.000100"].Equal(*activity) {
		t.Errorf("watermark = %v, want the activity time", next["slack:C1:1699999999.000100"])
	}
}

// A task with zero comments is reported as having no work log, and its links
// measure changed against the task's UpdatedAt.
func TestRunNoWorkLog(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-06-01T00:00:00Z"),
	}}

	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]Result{"slack:C1:1699999999.000100": {LastActivity: tp("2026-07-10T00:00:00Z")}},
	})

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	tr := report.Tasks["1"]
	if tr.HasWorkLog {
		t.Error("HasWorkLog = true, want false for a task with no comments")
	}
	if !tr.Links[0].Changed {
		t.Error("changed = false, want true measured against UpdatedAt")
	}
}

// A key the prober was asked about but omitted from its result is unchecked,
// never a silent no-change.
func TestRunUncheckedOnOmittedKey(t *testing.T) {
	tasks := []sources.Task{{
		ID:        "1",
		Title:     "x https://kong.slack.com/archives/C1/p1699999999000100",
		UpdatedAt: *tp("2026-07-01T00:00:00Z"),
	}}

	var reg Registry
	reg.Register(stubProber{system: links.SystemSlack, result: map[string]Result{}})

	report, next, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	link := report.Tasks["1"].Links[0]
	if !link.Unchecked {
		t.Errorf("link = %+v, want unchecked for an omitted key", link)
	}
	if _, ok := next[link.Key]; ok {
		t.Error("a watermark was written for an omitted (unchecked) key")
	}
}

// The per-system fan-out is capped: records beyond the cap render unchecked
// with ReasonTooMany and never reach the prober, so one task stuffed with
// references cannot amplify into an unbounded number of outbound requests.
func TestRunCapsPerSystemFanout(t *testing.T) {
	var title strings.Builder
	title.WriteString("bulk ")
	for i := 0; i < maxLinksPerSystem+5; i++ {
		fmt.Fprintf(&title, "https://kong.slack.com/archives/C1/p16999999990%05d ", i)
	}
	tasks := []sources.Task{{ID: "1", Title: title.String(), UpdatedAt: *tp("2026-07-01T00:00:00Z")}}

	seen := 0
	var reg Registry
	reg.Register(funcProber{
		system: links.SystemSlack,
		fn: func(ls []links.Link) map[string]Result {
			seen = len(ls)
			out := make(map[string]Result, len(ls))
			now := *tp("2026-07-10T00:00:00Z")
			for _, l := range ls {
				la := now
				out[l.Key()] = Result{LastActivity: &la}
			}
			return out
		},
	})

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if seen != maxLinksPerSystem {
		t.Errorf("prober received %d links, want the cap %d", seen, maxLinksPerSystem)
	}

	tooMany := 0
	for _, l := range report.Tasks["1"].Links {
		if l.Unchecked && l.Reason == ReasonTooMany {
			tooMany++
		}
	}
	if tooMany != 5 {
		t.Errorf("links over the cap = %d, want 5 rendered ReasonTooMany", tooMany)
	}
}

// funcProber runs an arbitrary function as its Probe body.
type funcProber struct {
	system links.System
	fn     func(ls []links.Link) map[string]Result
}

func (p funcProber) System() links.System { return p.system }

func (p funcProber) Probe(_ context.Context, ls []links.Link, _ sources.Watermark) (map[string]Result, error) {
	return p.fn(ls), nil
}

// The whole report golden-pins the JSON shape, including a no-work-log task.
func TestRunGolden(t *testing.T) {
	tasks := []sources.Task{
		{
			ID:        "8899",
			Title:     "Follow up https://kong.slack.com/archives/C1/p1699999999000100",
			UpdatedAt: *tp("2026-06-01T00:00:00Z"),
			Comments:  []sources.Comment{{PostedAt: *tp("2026-07-18T14:00:00Z")}},
		},
		{ID: "9001", Title: "Draft the brief", UpdatedAt: *tp("2026-07-01T00:00:00Z")},
	}

	var reg Registry
	reg.Register(stubProber{
		system: links.SystemSlack,
		result: map[string]Result{"slack:C1:1699999999.000100": {LastActivity: tp("2026-07-20T09:00:00Z")}},
	})

	report, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	rendered, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	golden.Assert(t, "engine.golden", string(rendered))
}
