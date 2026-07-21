package dispatch

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/queue"
	"github.com/bashfulrobot/ballpoint/internal/sources"
	"github.com/bashfulrobot/ballpoint/internal/store"
)

// recorder captures script argv from the fake RunScript.
type recorder struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recorder) run(_ context.Context, argv []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, argv)
	return nil
}

func baseConfig(t *testing.T, root string, rec *recorder, assess func(context.Context, string) (Assessment, float64, error)) Config {
	t.Helper()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveTask(sources.Task{ID: "42", Title: "t42"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Append(root, queue.Entry{ID: "e1", TaskID: "42", TaskRef: "id:42", Channel: "nudge", To: "#t", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := queue.Load(root)
	return Config{
		Store:       st,
		Root:        root,
		Report:      probe.Report{Tasks: map[string]probe.TaskReport{"42": {Title: "t42"}}},
		Entries:     entries,
		ScriptsDir:  "/scripts",
		Concurrency: 2,
		Now:         func() time.Time { return time.Unix(0, 0).UTC() },
		Assess:      assess,
		RunScript:   rec.run,
		Stdout:      io.Discard,
	}
}

func TestRunSuccessWritesBackAndDrains(t *testing.T) {
	root := t.TempDir()
	rec := &recorder{}
	cfg := baseConfig(t, root, rec, func(context.Context, string) (Assessment, float64, error) {
		return Assessment{Summary: "assessed"}, 0.01, nil
	})
	sum, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Succeeded != 1 {
		t.Errorf("summary = %+v, want 1 succeeded", sum)
	}
	if sum.CostUSD != 0.01 {
		t.Errorf("summary cost = %v, want 0.01", sum.CostUSD)
	}
	// Two script calls: the draft log then the assessment write. Drafts run
	// first so the assessment is the last write before the drain.
	if len(rec.calls) != 2 {
		t.Fatalf("script calls = %d, want 2 (%v)", len(rec.calls), rec.calls)
	}
	if rec.calls[0][0] != "/scripts/td_draft.sh" || rec.calls[1][0] != "/scripts/td_worklog.sh" {
		t.Errorf("call order = %v, want draft then worklog", rec.calls)
	}
	left, _ := queue.Load(root)
	if len(left) != 0 {
		t.Errorf("queue not drained: %+v", left)
	}
	got, _ := LoadStatuses(root)
	if len(got) != 1 || got[0].State != StateSucceeded {
		t.Errorf("status = %+v", got)
	}
}

func TestRunTwoTasksDrainWholeQueue(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"1", "2"} {
		if err := st.SaveTask(sources.Task{ID: id, Title: "t" + id}); err != nil {
			t.Fatal(err)
		}
		if err := queue.Append(root, queue.Entry{ID: "e" + id, TaskID: id, TaskRef: "id:" + id, Channel: "nudge", To: "#t", Body: "hi"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := queue.Load(root)
	rec := &recorder{}
	cfg := Config{
		Store:       st,
		Root:        root,
		Report:      probe.Report{Tasks: map[string]probe.TaskReport{}},
		Entries:     entries,
		ScriptsDir:  "/scripts",
		Concurrency: 2,
		Now:         func() time.Time { return time.Unix(0, 0).UTC() },
		Assess: func(context.Context, string) (Assessment, float64, error) {
			return Assessment{Summary: "assessed"}, 0.02, nil
		},
		RunScript: rec.run,
		Stdout:    io.Discard,
	}
	sum, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Succeeded != 2 {
		t.Errorf("summary = %+v, want 2 succeeded", sum)
	}
	// Both tasks drain concurrently. Remove serializes internally, so no
	// removal is lost and the queue ends empty.
	left, _ := queue.Load(root)
	if len(left) != 0 {
		t.Errorf("concurrent drain lost removals, queue = %+v", left)
	}
}

func TestRunFailureLeavesTaskUntouched(t *testing.T) {
	root := t.TempDir()
	rec := &recorder{}
	cfg := baseConfig(t, root, rec, func(context.Context, string) (Assessment, float64, error) {
		return Assessment{}, 0, errors.New("model down")
	})
	sum, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Failed != 1 {
		t.Errorf("summary = %+v, want 1 failed", sum)
	}
	if len(rec.calls) != 0 {
		t.Errorf("failed job still wrote back: %v", rec.calls)
	}
	left, _ := queue.Load(root)
	if len(left) != 1 {
		t.Errorf("failed job drained the queue: %+v", left)
	}
	got, _ := LoadStatuses(root)
	if len(got) != 1 || got[0].State != StateFailed {
		t.Errorf("status = %+v, want failed", got)
	}
}

func TestRunUsageLimitRequeues(t *testing.T) {
	root := t.TempDir()
	rec := &recorder{}
	cfg := baseConfig(t, root, rec, func(context.Context, string) (Assessment, float64, error) {
		return Assessment{}, 0, ErrUsageLimit
	})
	sum, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Requeued != 1 {
		t.Errorf("summary = %+v, want 1 requeued", sum)
	}
	left, _ := queue.Load(root)
	if len(left) != 1 {
		t.Errorf("requeued job drained the queue: %+v", left)
	}
	if len(rec.calls) != 0 {
		t.Errorf("requeued job wrote back: %v", rec.calls)
	}
}

func TestRunDryRunTouchesNothing(t *testing.T) {
	root := t.TempDir()
	rec := &recorder{}
	cfg := baseConfig(t, root, rec, func(context.Context, string) (Assessment, float64, error) {
		t.Fatal("dry run must not call the assessor")
		return Assessment{}, 0, nil
	})
	cfg.DryRun = true
	sum, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 {
		t.Errorf("summary = %+v, want 1 skipped", sum)
	}
	if len(rec.calls) != 0 {
		t.Errorf("dry run wrote back: %v", rec.calls)
	}
	left, _ := queue.Load(root)
	if len(left) != 1 {
		t.Errorf("dry run drained the queue: %+v", left)
	}
}
