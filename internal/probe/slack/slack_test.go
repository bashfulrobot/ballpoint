package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func mustTS(s string) time.Time {
	t, err := parseSlackTS(s)
	if err != nil {
		panic(err)
	}
	return t
}

// fakeSlack serves one channel with two threads. Thread A advanced past the
// watermark, thread B did not. It counts replies calls so the test can assert
// replies is fetched only for the advanced thread.
type fakeSlack struct{ repliesCalls int32 }

func (f *fakeSlack) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1699999999.000100", "thread_ts": "1699999999.000100",
						"reply_count": 3, "latest_reply": "1700000500.000000"},
					{"ts": "1699999999.000200", "thread_ts": "1699999999.000200",
						"reply_count": 1, "latest_reply": "1699999999.000300"},
				},
			})
		case "/conversations.replies":
			atomic.AddInt32(&f.repliesCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1700000500.000000"},
				},
			})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	})
}

func TestProbeCollapsesAndFetchesAdvancedOnly(t *testing.T) {
	fake := &fakeSlack{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))

	ls := []links.Link{
		{System: links.SystemSlack, Raw: "a", Record: "C1:1699999999.000100",
			Fields: map[string]string{"channel": "C1", "thread": "1699999999.000100"}},
		{System: links.SystemSlack, Raw: "b", Record: "C1:1699999999.000200",
			Fields: map[string]string{"channel": "C1", "thread": "1699999999.000200"}},
	}

	// Watermark: thread B is already at its latest_reply, thread A is behind.
	since := sources.Watermark{
		"slack:C1:1699999999.000100": time.Unix(1699999000, 0),
		"slack:C1:1699999999.000200": mustTS("1699999999.000300"),
	}

	out, err := c.Probe(context.Background(), ls, since)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}

	if got := atomic.LoadInt32(&fake.repliesCalls); got != 1 {
		t.Errorf("replies calls = %d, want 1 (only the advanced thread)", got)
	}
	if r := out["slack:C1:1699999999.000100"]; r.LastActivity == nil {
		t.Error("advanced thread has no last activity")
	}
	if r := out["slack:C1:1699999999.000200"]; r.Unchecked {
		t.Error("unadvanced thread should be checked (from history), not unchecked")
	}
}

// An expired token makes every slack link unchecked with ReasonAuth, never a
// silent no-change.
func TestProbeExpiredTokenUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemSlack, Record: "C1:1", Fields: map[string]string{"channel": "C1", "thread": "1"}}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["slack:C1:1"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
