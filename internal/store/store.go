package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Store roots ballpoint's cache and watermarks at a directory, normally
// config.StateDir().
type Store struct {
	root string
}

// Open ensures the store's directories exist and returns a Store rooted there.
func Open(root string) (*Store, error) {
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating cache directory %s: %w", cacheDir, err)
	}
	return &Store{root: root}, nil
}

func (s *Store) watermarkPath() string { return filepath.Join(s.root, "watermarks.json") }

// validTaskID rejects any id that is not safe as a filename. Task IDs come off
// the Todoist API, so a spoofed or drifted value containing a path separator or
// a traversal sequence must not be joined into a cache path. Todoist IDs are
// numeric today; this fails closed if that ever changes.
func validTaskID(id string) error {
	if id == "" {
		return errors.New("empty task id")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("task id %q is not a safe filename", id)
	}
	return nil
}

func (s *Store) taskPath(id string) (string, error) {
	if err := validTaskID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.root, "cache", id+".json"), nil
}

// LoadWatermark reads the watermark map. A missing file returns an empty map,
// so a first run fetches everything.
func (s *Store) LoadWatermark() (sources.Watermark, error) {
	data, err := os.ReadFile(s.watermarkPath())
	if errors.Is(err, os.ErrNotExist) {
		return sources.Watermark{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.watermarkPath(), err)
	}

	var w sources.Watermark
	if err := json.Unmarshal(data, &w); err != nil {
		// The watermark is a cache that a probe can always rebuild. A corrupt
		// file (a torn write from a power loss, say) should not wedge every
		// future run behind a manual delete, so treat it like a missing file:
		// warn, then start empty and re-fetch everything.
		log.Printf("store: watermark file %s is unreadable (%v); starting from empty", s.watermarkPath(), err)
		return sources.Watermark{}, nil
	}
	if w == nil {
		w = sources.Watermark{}
	}
	return w, nil
}

// SaveWatermark writes the watermark map atomically.
func (s *Store) SaveWatermark(w sources.Watermark) error {
	return writeAtomic(s.watermarkPath(), w)
}

// LoadTask reads a cached task. The bool is false when the task is not cached.
func (s *Store) LoadTask(id string) (sources.Task, bool, error) {
	path, err := s.taskPath(id)
	if err != nil {
		return sources.Task{}, false, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return sources.Task{}, false, nil
	}
	if err != nil {
		return sources.Task{}, false, fmt.Errorf("reading %s: %w", path, err)
	}

	var t sources.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return sources.Task{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	return t, true, nil
}

// SaveTask writes a task to the cache atomically.
func (s *Store) SaveTask(t sources.Task) error {
	path, err := s.taskPath(t.ID)
	if err != nil {
		return err
	}
	return writeAtomic(path, t)
}

// LoadAllTasks reads every cached task, so the TUI can walk the corpus offline.
// A malformed or unreadable entry is skipped rather than failing the whole walk,
// so one bad cache file does not blank the queue. Order is not guaranteed; the
// caller sorts.
func (s *Store) LoadAllTasks() ([]sources.Task, error) {
	cacheDir := filepath.Join(s.root, "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading cache directory %s: %w", cacheDir, err)
	}
	tasks := make([]sources.Task, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, ok, err := s.LoadTask(id)
		if err != nil || !ok {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *Store) reportPath() string { return filepath.Join(s.root, "report.json") }

// SaveReport writes the freshness report atomically, mirroring SaveWatermark.
// The TUI reads it to overlay per-link freshness on each card.
func (s *Store) SaveReport(r probe.Report) error {
	return writeAtomic(s.reportPath(), r)
}

// LoadReport reads the freshness report. ok is false when no probe has written
// one yet, so a first run before any probe still opens (with no freshness data)
// rather than erroring.
func (s *Store) LoadReport() (probe.Report, bool, error) {
	data, err := os.ReadFile(s.reportPath())
	if errors.Is(err, os.ErrNotExist) {
		return probe.Report{}, false, nil
	}
	if err != nil {
		return probe.Report{}, false, fmt.Errorf("reading %s: %w", s.reportPath(), err)
	}
	var r probe.Report
	if err := json.Unmarshal(data, &r); err != nil {
		return probe.Report{}, false, fmt.Errorf("parsing %s: %w", s.reportPath(), err)
	}
	return r, true, nil
}

// writeAtomic marshals v and writes it by creating a temp file in the target's
// directory, flushing it to disk, then renaming over the target. The rename is
// atomic at the VFS layer, so a killed process never leaves a torn file, and
// the fsync before it closes the power-loss window where the rename is durable
// but the contents are not.
func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	// Cleanup on any early return; a no-op after a successful rename.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		// The write error is the one worth returning; the close is cleanup.
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}
