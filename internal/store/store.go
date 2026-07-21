package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

func (s *Store) taskPath(id string) string {
	return filepath.Join(s.root, "cache", id+".json")
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
		return nil, fmt.Errorf("parsing %s: %w", s.watermarkPath(), err)
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
	data, err := os.ReadFile(s.taskPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return sources.Task{}, false, nil
	}
	if err != nil {
		return sources.Task{}, false, fmt.Errorf("reading %s: %w", s.taskPath(id), err)
	}

	var t sources.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return sources.Task{}, false, fmt.Errorf("parsing %s: %w", s.taskPath(id), err)
	}
	return t, true, nil
}

// SaveTask writes a task to the cache atomically.
func (s *Store) SaveTask(t sources.Task) error {
	return writeAtomic(s.taskPath(t.ID), t)
}

// writeAtomic marshals v and writes it by creating a temp file in the target's
// directory and renaming over the target, so a killed process never leaves a
// torn file.
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
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, path, err)
	}
	return nil
}
