package probe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// countingProber records how many times Probe is invoked, standing in for a
// real source with no network.
type countingProber struct {
	system links.System
	calls  *int
}

func (p countingProber) System() links.System { return p.system }

func (p countingProber) Probe(_ context.Context, ls []links.Link, _ sources.Watermark) (map[string]Result, error) {
	*p.calls++
	out := make(map[string]Result, len(ls))
	now := time.Unix(1721470000, 0).UTC()
	for _, l := range ls {
		la := now
		out[l.Key()] = Result{LastActivity: &la}
	}
	return out, nil
}

// TestBatchBySystemCollapsesCalls builds a synthetic corpus modelled on the
// real one (71 tasks, ~148 links, Slack concentrated on ~40 channels) and shows
// batch-by-system issues one prober call per system rather than one per link.
func TestBatchBySystemCollapsesCalls(t *testing.T) {
	tasks := syntheticCorpus()

	totalLinks := 0
	for _, task := range tasks {
		totalLinks += len(links.Extract(task))
	}
	if totalLinks < 140 {
		t.Fatalf("synthetic corpus has %d links, want ~148", totalLinks)
	}

	slackCalls, ahaCalls, driveCalls := 0, 0, 0
	var reg Registry
	reg.Register(countingProber{system: links.SystemSlack, calls: &slackCalls})
	reg.Register(countingProber{system: links.SystemAha, calls: &ahaCalls})
	reg.Register(countingProber{system: links.SystemGDrive, calls: &driveCalls})

	if _, _, err := Run(context.Background(), tasks, sources.Watermark{}, &reg); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	proberCalls := slackCalls + ahaCalls + driveCalls
	t.Logf("corpus: %d tasks, %d links; prober invocations: %d (slack %d, aha %d, drive %d)",
		len(tasks), totalLinks, proberCalls, slackCalls, ahaCalls, driveCalls)

	// One invocation per registered system with links, far below one per link.
	if proberCalls > 3 {
		t.Errorf("prober invocations = %d, want one per system (batch-by-system)", proberCalls)
	}
	if totalLinks <= proberCalls {
		t.Errorf("no collapse: %d links, %d calls", totalLinks, proberCalls)
	}
}

// syntheticCorpus returns 71 tasks carrying 148 links, with Slack links spread
// over 40 channels, matching the shape the issue measured.
func syntheticCorpus() []sources.Task {
	var tasks []sources.Task
	link := 0
	for i := 0; i < 71; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "task %d\n", i)
		// Slack: up to 129 links across 40 channels.
		for j := 0; j < 2 && link < 129; j++ {
			ch := fmt.Sprintf("C%d", link%40)
			fmt.Fprintf(&b, "https://kong.slack.com/archives/%s/p16999999990%05d\n", ch, link)
			link++
		}
		// A handful of aha and drive links to exercise the other systems.
		if i%7 == 0 {
			fmt.Fprintf(&b, "GTWY-I-%d\n", 1000+i)
		}
		if i%9 == 0 {
			fmt.Fprintf(&b, "https://docs.google.com/document/d/FILE%d/edit\n", i)
		}
		tasks = append(tasks, sources.Task{ID: fmt.Sprintf("%d", i), Title: b.String(), UpdatedAt: time.Unix(1719000000, 0).UTC()})
	}
	return tasks
}
