package tui

import (
	"reflect"
	"testing"
)

func TestBuildArgvReason(t *testing.T) {
	v, _ := VerbForName("done")
	got := BuildArgv(v, "id:9", "shipped the fix")
	want := []string{"id:9", "--reason", "shipped the fix"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv done = %v, want %v", got, want)
	}
}

func TestBuildArgvEntry(t *testing.T) {
	v, _ := VerbForName("log")
	got := BuildArgv(v, "id:9", "chased eng")
	want := []string{"id:9", "--entry", "chased eng"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv log = %v, want %v", got, want)
	}
}

func TestBuildArgvPositional(t *testing.T) {
	v, _ := VerbForName("col")
	got := BuildArgv(v, "id:9", "Engineering")
	want := []string{"id:9", "Engineering"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv col = %v, want %v", got, want)
	}
}

func TestBuildArgvMerge(t *testing.T) {
	v, _ := VerbForName("merge")
	got := BuildArgv(v, "id:survivor", "id:1 id:2 id:3")
	want := []string{"--survivor", "id:1", "--loser", "id:2", "--loser", "id:3", "--reason", "merged during triage walk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv merge = %v, want %v", got, want)
	}
}
