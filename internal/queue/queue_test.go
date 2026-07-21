package queue

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRemoveDropsSelectedEntriesInOrder(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"a", "b", "c"} {
		if err := Append(root, Entry{ID: id, TaskID: id}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Remove(root, map[string]bool{"b": true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("removed = %d, want 1", n)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("remaining = %+v, want [a c] in order", got)
	}
}

func TestRemoveMissingFileIsNoOp(t *testing.T) {
	root := t.TempDir()
	n, err := Remove(root, map[string]bool{"x": true})
	if err != nil {
		t.Fatalf("Remove on empty queue: %v", err)
	}
	if n != 0 {
		t.Errorf("removed = %d, want 0", n)
	}
}

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

// Append and Remove run from separate processes (the TUI and the dispatcher)
// while a walk is in flight, so an append must never be lost to a concurrent
// rewriting drain. Seed a set of entries, then concurrently append fresh ones
// while draining every seed. The queue must end holding exactly the appended
// entries, none clobbered by a racing Remove.
func TestConcurrentAppendAndRemoveLosesNothing(t *testing.T) {
	root := t.TempDir()
	const n = 40
	seeds := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("seed-%d", i)
		seeds[id] = true
		if err := Append(root, Entry{ID: id, TaskID: id}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		id := fmt.Sprintf("seed-%d", i)
		fresh := fmt.Sprintf("fresh-%d", i)
		go func() {
			defer wg.Done()
			if err := Append(root, Entry{ID: fresh, TaskID: fresh}); err != nil {
				t.Errorf("append: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := Remove(root, map[string]bool{id: true}); err != nil {
				t.Errorf("remove: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("queue holds %d entries, want %d (a concurrent append was lost)", len(got), n)
	}
	for _, e := range got {
		if seeds[e.ID] {
			t.Errorf("seed %q survived the drain", e.ID)
		}
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
