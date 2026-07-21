package store

import (
	"os"
	"path/filepath"
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

// A task ID from the API that is not a safe filename must be rejected before it
// reaches a path, so a spoofed or drifted ID cannot write outside the cache.
func TestUnsafeTaskIDRejected(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	unsafe := []string{"../escape", "sub/dir", `back\slash`, ".", "..", ".hidden", ""}
	for _, id := range unsafe {
		t.Run(id, func(t *testing.T) {
			if err := s.SaveTask(sources.Task{ID: id, Title: "x"}); err == nil {
				t.Errorf("SaveTask(id=%q) error = nil, want a rejection", id)
			}
			if _, _, err := s.LoadTask(id); err == nil {
				t.Errorf("LoadTask(id=%q) error = nil, want a rejection", id)
			}
		})
	}

	// A traversal attempt must not have written anything above the cache dir.
	if _, err := os.Stat(filepath.Join(root, "escape.json")); !os.IsNotExist(err) {
		t.Errorf("a traversal id wrote outside the cache directory: stat err = %v", err)
	}
}

// A corrupt watermark file is a rebuildable cache, so it loads as empty and
// warns rather than wedging every future run behind a manual delete.
func TestLoadWatermarkCorruptRecovers(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "watermarks.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing corrupt watermark: %v", err)
	}

	got, err := s.LoadWatermark()
	if err != nil {
		t.Fatalf("LoadWatermark() error = %v, want nil (recover to empty)", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadWatermark() = %v, want empty after corruption", got)
	}
}
