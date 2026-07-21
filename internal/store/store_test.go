package store

import (
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestWatermarkRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	want := sources.Watermark{
		"todoist:1": time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		"todoist:2": time.Date(2026, 7, 19, 8, 30, 0, 0, time.UTC),
	}

	if err := s.SaveWatermark(want); err != nil {
		t.Fatalf("SaveWatermark() error = %v", err)
	}

	got, err := s.LoadWatermark()
	if err != nil {
		t.Fatalf("LoadWatermark() error = %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("LoadWatermark() returned %d entries, want %d", len(got), len(want))
	}
	for k, wv := range want {
		if !got[k].Equal(wv) {
			t.Errorf("LoadWatermark()[%q] = %v, want %v", k, got[k], wv)
		}
	}
}

// A first run has no watermark file. That is not an error; it loads empty so
// the probe fetches everything.
func TestLoadWatermarkMissingIsEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	got, err := s.LoadWatermark()
	if err != nil {
		t.Fatalf("LoadWatermark() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadWatermark() = %v, want empty", got)
	}
}

func TestTaskRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	want := sources.Task{
		ID:       "42",
		Title:    "ship the client",
		Priority: "p1",
		Comments: []sources.Comment{{ID: "c1", Content: "note"}},
	}

	if err := s.SaveTask(want); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}

	got, ok, err := s.LoadTask("42")
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if !ok {
		t.Fatal("LoadTask() ok = false, want true")
	}
	if got.Title != want.Title || got.Priority != want.Priority || len(got.Comments) != 1 {
		t.Errorf("LoadTask() = %+v, want %+v", got, want)
	}
}

func TestLoadTaskMissing(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	_, ok, err := s.LoadTask("nope")
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if ok {
		t.Error("LoadTask() ok = true for a missing task, want false")
	}
}
