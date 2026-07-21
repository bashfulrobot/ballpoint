package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Session is the resume state: the active scope and the current cursor task, so
// quitting and relaunching returns to the same place.
type Session struct {
	Scope  Scope  `json:"scope"`
	Cursor string `json:"cursor"` // the current task id
}

func sessionPath(root string) string { return filepath.Join(root, "session.json") }

// SaveSession writes the resume state atomically.
func SaveSession(root string, s Session) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session: %w", err)
	}
	path := sessionPath(root)
	tmp, err := os.CreateTemp(root, "session.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp session: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing session: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming session: %w", err)
	}
	return nil
}

// LoadSession reads the resume state. ok is false when none has been written.
func LoadSession(root string) (Session, bool, error) {
	data, err := os.ReadFile(sessionPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("reading session: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, false, fmt.Errorf("decoding session: %w", err)
	}
	return s, true, nil
}

// ResolveCursor returns the index of the cursor task within order, or 0 (the
// start) when the task is no longer present. Nothing is hidden either way; the
// walk just resumes from a valid position.
func ResolveCursor(order []string, cursor string) int {
	for i, id := range order {
		if id == cursor {
			return i
		}
	}
	return 0
}
