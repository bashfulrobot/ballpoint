package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/bashfulrobot/ballpoint/internal/fsutil"
)

// Size caps on the assessment summary. maxAssessmentBytes bounds the model's
// summary at persist and load time so a pathological or tampered summary cannot
// bloat memory or the interactive render. maxStatusFileBytes bounds a single
// status file read, so a tampered oversized file is capped rather than slurped
// whole; a file larger than this fails to decode and is skipped.
const (
	maxAssessmentBytes = 8 << 10 // 8 KiB
	maxStatusFileBytes = 1 << 20 // 1 MiB
)

// truncateAssessment caps s at maxAssessmentBytes without splitting a UTF-8
// rune. An over-long summary is trimmed rather than rejected, so the card still
// shows the model's take, just bounded.
func truncateAssessment(s string) string {
	if len(s) <= maxAssessmentBytes {
		return s
	}
	b := s[:maxAssessmentBytes]
	// Drop a trailing incomplete rune so the trimmed tail is valid UTF-8. A byte
	// slice cut mid-rune ends in a lead or continuation byte that DecodeLastRune
	// reports as RuneError with size 1.
	for len(b) > 0 {
		if r, size := utf8.DecodeLastRuneInString(b); r == utf8.RuneError && size <= 1 {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}

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
	// Bound the summary before it lands on disk, so a pathological model output
	// cannot bloat the status file or the walk that later renders it.
	s.Assessment = truncateAssessment(s.Assessment)
	return fsutil.WriteJSONAtomic(path, s)
}

// readStatus reads and decodes one status file. ok is false when the file is
// missing, unreadable, or malformed, so a single bad file is skipped rather than
// failing the whole query. The open uses O_NOFOLLOW so a symlink planted in the
// 0o700 dispatch dir cannot redirect the read outside it, and the read is capped
// at maxStatusFileBytes so a tampered oversized file is bounded rather than
// slurped whole (an oversized file then fails to decode and is skipped).
func readStatus(path string) (Status, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Status{}, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxStatusFileBytes))
	if err != nil {
		return Status{}, false
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return Status{}, false
	}
	// Defensively bound a tampered summary that decoded within the file cap.
	s.Assessment = truncateAssessment(s.Assessment)
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
