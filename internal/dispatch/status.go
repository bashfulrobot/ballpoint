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
// <root>/dispatch/. Assessment carries the model's summary line on a succeeded
// job, so the walk can show the AI's take per card without a Todoist round trip;
// it is empty on any non-succeeded state.
type Status struct {
	TaskID     string    `json:"task_id"`
	TaskRef    string    `json:"task_ref"`
	State      string    `json:"state"`
	Detail     string    `json:"detail,omitempty"`
	Assessment string    `json:"assessment,omitempty"`
	CostUSD    float64   `json:"cost_usd,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
}

func statusDir(root string) string { return filepath.Join(root, "dispatch") }

func statusPath(root, id string) (string, error) {
	if err := fsutil.SafeFilename(id); err != nil {
		return "", err
	}
	return filepath.Join(statusDir(root), id+".json"), nil
}

// WriteStatus writes one task's status atomically, overwriting any prior state
// for the same task. A write whose Assessment is empty carries a prior non-empty
// summary forward, so a failed or requeued re-run does not wipe the last good
// assessment the walk shows. The success path always sets a non-empty summary,
// so it still overwrites.
func WriteStatus(root string, s Status) error {
	path, err := statusPath(root, s.TaskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(statusDir(root), 0o700); err != nil {
		return fmt.Errorf("creating dispatch directory: %w", err)
	}
	if s.Assessment == "" {
		if prior, ok := readStatus(path); ok && prior.Assessment != "" {
			s.Assessment = prior.Assessment
		}
	}
	return fsutil.WriteJSONAtomic(path, s)
}

// readStatus reads and decodes one status file. ok is false when the file is
// missing, unreadable, or malformed, so a single bad file is skipped rather than
// failing the whole query.
func readStatus(path string) (Status, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, false
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, false
	}
	return s, true
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
		// A missing, unreadable, or malformed status file is skipped rather than
		// failing the whole query, so one bad file does not drop every assessment.
		if s, ok := readStatus(filepath.Join(statusDir(root), e.Name())); ok {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}
