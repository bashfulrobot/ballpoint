package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestStatusRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := Status{TaskID: "42", TaskRef: "id:42", State: StateSucceeded, CostUSD: 0.01, StartedAt: time.Unix(1, 0).UTC()}
	if err := WriteStatus(root, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStatuses(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TaskID != "42" || got[0].State != StateSucceeded {
		t.Errorf("statuses = %+v", got)
	}
}

func TestWriteStatusOverwritesSameTask(t *testing.T) {
	root := t.TempDir()
	_ = WriteStatus(root, Status{TaskID: "42", State: StateRunning})
	_ = WriteStatus(root, Status{TaskID: "42", State: StateFailed, Detail: "boom"})
	got, _ := LoadStatuses(root)
	if len(got) != 1 || got[0].State != StateFailed || got[0].Detail != "boom" {
		t.Errorf("statuses = %+v, want a single failed entry", got)
	}
}

func TestLoadStatusesEmpty(t *testing.T) {
	got, err := LoadStatuses(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("statuses = %+v, want empty", got)
	}
}

func TestWriteStatusRejectsUnsafeID(t *testing.T) {
	if err := WriteStatus(t.TempDir(), Status{TaskID: "../evil"}); err == nil {
		t.Error("unsafe task id should be rejected")
	}
}

// A non-success re-run with no assessment preserves the last good summary, so a
// failed or requeued retry does not blank out what the walk shows. A later
// success overwrites it.
func TestWriteStatusCarriesAssessmentForward(t *testing.T) {
	root := t.TempDir()
	_ = WriteStatus(root, Status{TaskID: "42", State: StateSucceeded, Assessment: "waiting on legal"})
	// A requeue (usage limit) writes an empty assessment; the prior must survive.
	_ = WriteStatus(root, Status{TaskID: "42", State: StateRequeued, Detail: "usage limit"})
	got, _ := LoadStatuses(root)
	if len(got) != 1 || got[0].State != StateRequeued {
		t.Fatalf("statuses = %+v, want a single requeued entry", got)
	}
	if got[0].Assessment != "waiting on legal" {
		t.Errorf("assessment = %q, want it carried forward", got[0].Assessment)
	}
	// A later success replaces the summary rather than carrying the old one.
	_ = WriteStatus(root, Status{TaskID: "42", State: StateSucceeded, Assessment: "resolved"})
	got, _ = LoadStatuses(root)
	if got[0].Assessment != "resolved" {
		t.Errorf("assessment = %q, want the new summary to overwrite", got[0].Assessment)
	}
}

// One unreadable or malformed status file is skipped, not fatal, so a single bad
// file does not drop every other task's assessment.
func TestLoadStatusesSkipsBadFile(t *testing.T) {
	root := t.TempDir()
	_ = WriteStatus(root, Status{TaskID: "42", State: StateSucceeded, Assessment: "good"})
	// Drop a garbage .json alongside the good one.
	if err := os.WriteFile(filepath.Join(statusDir(root), "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStatuses(root)
	if err != nil {
		t.Fatalf("LoadStatuses returned error for a bad file: %v", err)
	}
	if len(got) != 1 || got[0].TaskID != "42" {
		t.Errorf("statuses = %+v, want only the good entry", got)
	}
}

func TestWriteStatusBoundsAssessment(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("a", maxAssessmentBytes+4096)
	if err := WriteStatus(root, Status{TaskID: "42", State: StateSucceeded, Assessment: big}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadStatuses(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("statuses = %+v, want one entry", got)
	}
	if n := len(got[0].Assessment); n > maxAssessmentBytes {
		t.Errorf("persisted assessment = %d bytes, want <= %d", n, maxAssessmentBytes)
	}
}

func TestTruncateAssessmentKeepsRuneBoundary(t *testing.T) {
	// A multi-byte rune straddling the cap must not be split into invalid UTF-8.
	s := strings.Repeat("a", maxAssessmentBytes-1) + "é" + "tail"
	got := truncateAssessment(s)
	if len(got) > maxAssessmentBytes {
		t.Errorf("len = %d, want <= %d", len(got), maxAssessmentBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncated assessment is not valid UTF-8: %q", got)
	}
}

func TestReadStatusRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	// Write a real status elsewhere, then point a .json symlink at it inside the
	// dispatch dir. The O_NOFOLLOW read must refuse to follow the link.
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"task_id":"99","state":"succeeded"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statusDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(statusDir(root), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, ok := readStatus(link); ok {
		t.Error("readStatus followed a symlink, want it rejected")
	}
}
