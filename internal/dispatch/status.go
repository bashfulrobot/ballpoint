package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/fsutil"
)

// Job states, persisted so `ballpoint dispatch --status` can report a run.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateRequeued  = "requeued"
	StateSkipped   = "skipped"
)

// Status is one task's dispatch outcome, one file per task under
// <root>/dispatch/.
type Status struct {
	TaskID    string    `json:"task_id"`
	TaskRef   string    `json:"task_ref"`
	State     string    `json:"state"`
	Detail    string    `json:"detail,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

func statusDir(root string) string { return filepath.Join(root, "dispatch") }

// safeID rejects a task id that is not usable as a filename, matching the
// store's discipline so a drifted id cannot escape the dispatch directory.
func safeID(id string) error {
	if id == "" {
		return errors.New("empty task id")
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("task id %q is not a safe filename", id)
	}
	return nil
}

func statusPath(root, id string) (string, error) {
	if err := safeID(id); err != nil {
		return "", err
	}
	return filepath.Join(statusDir(root), id+".json"), nil
}

// WriteStatus writes one task's status atomically, overwriting any prior state
// for the same task.
func WriteStatus(root string, s Status) error {
	path, err := statusPath(root, s.TaskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(statusDir(root), 0o700); err != nil {
		return fmt.Errorf("creating dispatch directory: %w", err)
	}
	return fsutil.WriteJSONAtomic(path, s)
}

// LoadStatuses reads every status file, sorted by task id. A missing directory
// is an empty result.
func LoadStatuses(root string) ([]Status, error) {
	ents, err := os.ReadDir(statusDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading dispatch directory: %w", err)
	}
	var out []Status
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(statusDir(root), e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading status %s: %w", e.Name(), err)
		}
		var s Status
		if err := json.Unmarshal(data, &s); err != nil {
			// A malformed status file is skipped rather than failing the query.
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}
