package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteBytesAtomicCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "f.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteBytesAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := WriteBytesAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "two" {
		t.Errorf("content = %q, want two", got)
	}
	// No leftover temp files in the directory.
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Errorf("directory has %d entries, want 1 (leftover temp file?)", len(ents))
	}
}

func TestWriteJSONAtomicRoundTrips(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "v.json")
	if err := WriteJSONAtomic(p, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\n  \"a\": 1\n}" {
		t.Errorf("json = %q", got)
	}
}
