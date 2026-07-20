package config

import (
	"path/filepath"
	"testing"
)

func TestStateDir(t *testing.T) {
	home := t.TempDir()
	fallback := filepath.Join(home, ".local", "state", "ballpoint")

	tests := []struct {
		name      string
		stateHome string
		want      string
	}{
		{
			name:      "absolute XDG_STATE_HOME is honoured",
			stateHome: "/var/lib/example",
			want:      filepath.Join("/var/lib/example", "ballpoint"),
		},
		{
			name:      "unset falls back to the spec default",
			stateHome: "",
			want:      fallback,
		},
		{
			name:      "relative XDG_STATE_HOME is ignored",
			stateHome: "relative/state",
			want:      fallback,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", tt.stateHome)

			got, err := StateDir()
			if err != nil {
				t.Fatalf("StateDir() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("StateDir() = %q, want %q", got, tt.want)
			}
		})
	}
}
