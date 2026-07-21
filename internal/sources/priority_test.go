package sources

import "testing"

func TestNormalizePriority(t *testing.T) {
	tests := []struct {
		name string
		api  int
		want string
	}{
		{name: "urgent maps to p1", api: 4, want: "p1"},
		{name: "high maps to p2", api: 3, want: "p2"},
		{name: "medium maps to p3", api: 2, want: "p3"},
		{name: "none maps to p4", api: 1, want: "p4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePriority(tt.api); got != tt.want {
				t.Errorf("NormalizePriority(%d) = %q, want %q", tt.api, got, tt.want)
			}
		})
	}
}

// The API only ever returns 1 through 4. Anything else is clamped into range
// rather than producing p0 or p5, so a caller never sees an impossible band.
func TestNormalizePriorityClamps(t *testing.T) {
	if got := NormalizePriority(0); got != "p4" {
		t.Errorf("NormalizePriority(0) = %q, want p4", got)
	}
	if got := NormalizePriority(9); got != "p1" {
		t.Errorf("NormalizePriority(9) = %q, want p1", got)
	}
}
