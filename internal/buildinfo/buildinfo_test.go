package buildinfo

import (
	"testing"

	// Registers -update so `go test ./... -update` works here too.
	_ "github.com/bashfulrobot/ballpoint/internal/golden"
)

func TestString(t *testing.T) {
	tests := []struct {
		name    string
		stamped string
		want    string
	}{
		{name: "link time stamp is used verbatim", stamped: "1.2.3", want: "1.2.3"},
		{name: "prerelease stamp is used verbatim", stamped: "0.1.0-rc1", want: "0.1.0-rc1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := Version
			t.Cleanup(func() { Version = original })

			Version = tt.stamped

			if got := String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An unstamped build must still report something a human can act on, so
// `go run` output is never an empty string.
func TestStringUnstamped(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	Version = ""

	if got := String(); got == "" {
		t.Error("String() = \"\", want a non-empty fallback")
	}
}
