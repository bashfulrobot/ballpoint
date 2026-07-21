package queue

import (
	"testing"
	"time"
)

func TestAppendAndLoad(t *testing.T) {
	root := t.TempDir()
	e1 := Entry{ID: "1-1", TaskID: "1", TaskRef: "DEVP-I-42", Channel: "slack", To: "#team", Body: "ping", QueuedAt: time.Unix(1, 0).UTC()}
	e2 := Entry{ID: "2-1", TaskID: "2", TaskRef: "CASE 5", Channel: "email", To: "x@y.z", Body: "hi", QueuedAt: time.Unix(2, 0).UTC()}
	if err := Append(root, e1); err != nil {
		t.Fatal(err)
	}
	if err := Append(root, e2); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "1-1" || got[1].Channel != "email" {
		t.Fatalf("Load() = %+v, want the two appended entries in order", got)
	}
}

func TestLoadEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() on empty error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load() = %d, want 0", len(got))
	}
}

// A body with an embedded newline must survive the round trip, since JSON
// escapes it and the reader splits on physical lines.
func TestAppendPreservesMultilineBody(t *testing.T) {
	root := t.TempDir()
	want := "line one\nline two"
	if err := Append(root, Entry{ID: "1-1", Body: want}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != want {
		t.Fatalf("Load() body = %q, want %q", got[0].Body, want)
	}
}
