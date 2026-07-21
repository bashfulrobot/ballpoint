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
	// The current card (id:survivor) is the survivor; the typed refs are losers.
	got := BuildArgv(v, "id:survivor", "id:1 id:2")
	want := []string{"--survivor", "id:survivor", "--loser", "id:1", "--loser", "id:2", "--reason", "merged during triage walk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv merge = %v, want %v", got, want)
	}
}

func TestBuildArgvLink(t *testing.T) {
	v, _ := VerbForName("link")
	got := BuildArgv(v, "id:9", "https://x.test/1 the ticket")
	want := []string{"id:9", "--link", "the ticket=https://x.test/1", "--entry", "linked the ticket"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgv link = %v, want %v", got, want)
	}
}
