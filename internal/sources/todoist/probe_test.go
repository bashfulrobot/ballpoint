package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/golden"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// fakeTodoist serves a small fixed scope: two tasks, one comment each, one
// project, one section. It records max in-flight comment requests so a test
// can assert the fetch is concurrent.
type fakeTodoist struct {
	inFlight    int32
	maxInFlight int32
}

func (f *fakeTodoist) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "1", "content": "first", "project_id": "p1", "priority": 4,
						"added_at": "2026-07-18T12:00:00Z", "updated_at": "2026-07-20T09:00:00Z"},
					{"id": "2", "content": "second", "project_id": "p1", "section_id": "s1", "priority": 1,
						"added_at": "2026-07-17T12:00:00Z", "updated_at": "2026-07-19T08:00:00Z"},
				},
				"next_cursor": nil,
			})
		case "/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "p1", "name": "Inbox"}}, "next_cursor": nil,
			})
		case "/sections":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "s1", "name": "Doing"}}, "next_cursor": nil,
			})
		case "/comments":
			n := atomic.AddInt32(&f.inFlight, 1)
			for {
				old := atomic.LoadInt32(&f.maxInFlight)
				if n <= old || atomic.CompareAndSwapInt32(&f.maxInFlight, old, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&f.inFlight, -1)

			id := r.URL.Query().Get("task_id")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"id": "c" + id, "content": "comment for " + id, "posted_at": "2026-07-20T10:00:00Z"},
				},
				"next_cursor": nil,
			})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	})
}

func TestProbeFetchesAndNormalises(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithVersion("9.9.9"))

	delta, err := c.Probe(context.Background(), sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if len(delta.Tasks) != 2 {
		t.Fatalf("Probe() returned %d tasks, want 2", len(delta.Tasks))
	}

	byID := map[string]sources.Task{}
	for _, task := range delta.Tasks {
		byID[task.ID] = task
	}
	if byID["1"].Priority != "p1" {
		t.Errorf("task 1 priority = %q, want p1", byID["1"].Priority)
	}
	if byID["1"].Project != "Inbox" {
		t.Errorf("task 1 project = %q, want Inbox", byID["1"].Project)
	}
	if byID["2"].Section != "Doing" {
		t.Errorf("task 2 section = %q, want Doing", byID["2"].Section)
	}
	if len(byID["1"].Comments) != 1 || byID["1"].Comments[0].Content != "comment for 1" {
		t.Errorf("task 1 comments = %+v, want one comment for 1", byID["1"].Comments)
	}

	rendered, err := json.MarshalIndent(delta, "", "  ")
	if err != nil {
		t.Fatalf("marshalling delta: %v", err)
	}
	golden.Assert(t, "probe.golden", string(rendered))
}

// Both comment fetches must overlap, proving the bounded group runs them
// concurrently rather than one after another.
func TestProbeFetchesCommentsConcurrently(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithConcurrency(12))

	if _, err := c.Probe(context.Background(), sources.Watermark{}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if atomic.LoadInt32(&fake.maxInFlight) < 2 {
		t.Errorf("max in-flight comment requests = %d, want at least 2 (concurrent)", fake.maxInFlight)
	}
}

// Changed holds a link key whose updated_at is after the incoming watermark;
// an up-to-date task is absent from Changed.
func TestProbeComputesChanged(t *testing.T) {
	fake := &fakeTodoist{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	since := sources.Watermark{
		"todoist:1": time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC), // equal to task 1, so unchanged
		// task 2 absent, so it counts as changed
	}

	delta, err := c.Probe(context.Background(), since)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	changed := map[string]bool{}
	for _, k := range delta.Changed {
		changed[k] = true
	}
	if changed["todoist:1"] {
		t.Error("todoist:1 in Changed, want absent (watermark equal to updated_at)")
	}
	if !changed["todoist:2"] {
		t.Error("todoist:2 not in Changed, want present (absent from watermark)")
	}
	if !delta.Next["todoist:1"].Equal(time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("Next[todoist:1] = %v, want the task's updated_at", delta.Next["todoist:1"])
	}
}
