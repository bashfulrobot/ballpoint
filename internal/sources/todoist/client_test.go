package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetAllPaginates(t *testing.T) {
	// Two pages: the first returns a cursor, the second returns none.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("User-Agent is empty, want a ballpoint agent")
		}

		cursor := r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		if cursor == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results":     []map[string]string{{"id": "1"}},
				"next_cursor": "PAGE2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":     []map[string]string{{"id": "2"}},
			"next_cursor": nil,
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL), WithVersion("9.9.9"))

	var out []rawNamed
	if err := c.getAll(context.Background(), "/projects", nil, &out); err != nil {
		t.Fatalf("getAll() error = %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("getAll() returned %d items, want 2 across both pages", len(out))
	}
	if out[0].ID != "1" || out[1].ID != "2" {
		t.Errorf("getAll() ids = %q,%q, want 1,2", out[0].ID, out[1].ID)
	}
}

func TestGetAllSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	var out []rawNamed
	err := c.getAll(context.Background(), "/projects", nil, &out)
	if err == nil {
		t.Fatal("getAll() error = nil, want a 401 error")
	}
}

// The default concurrency limit is 12 when no option overrides it.
func TestDefaultLimit(t *testing.T) {
	c := New("test-token")
	if c.limit != 12 {
		t.Errorf("default limit = %d, want 12", c.limit)
	}
}
