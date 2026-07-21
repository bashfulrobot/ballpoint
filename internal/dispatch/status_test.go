package dispatch

import (
	"testing"
	"time"
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
