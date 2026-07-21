package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// scopeServer serves n tasks and sleeps perCall on every request, standing in
// for real API latency without a token or the network.
func scopeServer(n int, perCall time.Duration) http.Handler {
	tasks := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%d", i+1)
		tasks[i] = map[string]any{
			"id": id, "content": "task " + id, "project_id": "p1", "priority": 1,
			"added_at": "2026-07-18T12:00:00Z", "updated_at": "2026-07-20T09:00:00Z",
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perCall)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": tasks, "next_cursor": nil})
		case "/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]string{{"id": "p1", "name": "Inbox"}}, "next_cursor": nil})
		case "/sections":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]string{}, "next_cursor": nil})
		case "/comments":
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}, "next_cursor": nil})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	})
}

func fetchWall(t *testing.T, baseURL string, concurrency int) time.Duration {
	t.Helper()
	// Rate limiting is disabled so this measures the concurrency mechanism
	// alone. With the limiter engaged both runs would be throttled to the same
	// requests-per-second floor and the ratio would collapse.
	c := New("test-token", WithBaseURL(baseURL), WithConcurrency(concurrency), WithRateLimit(0, 0))
	start := time.Now()
	delta, err := c.Probe(context.Background(), sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if len(delta.Tasks) != 71 {
		t.Fatalf("Probe() returned %d tasks, want 71", len(delta.Tasks))
	}
	return time.Since(start)
}

// TestConcurrencySpeedup proves bounded concurrency removes the sequential
// cost. With 71 tasks and a 10 ms per-call latency, the sequential comment
// fetch is ~710 ms while the 12-way fetch is ~60 ms. Requiring a 4x speedup
// leaves wide margin against CI scheduling noise.
func TestConcurrencySpeedup(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive, skipped under -short")
	}

	srv := httptest.NewServer(scopeServer(71, 10*time.Millisecond))
	defer srv.Close()

	seq := fetchWall(t, srv.URL, 1)
	conc := fetchWall(t, srv.URL, 12)

	t.Logf("71 task fetch: sequential %v, 12-way %v, speedup %.1fx", seq, conc, float64(seq)/float64(conc))

	if conc*4 > seq {
		t.Errorf("12-way fetch %v not 4x faster than sequential %v", conc, seq)
	}
}
