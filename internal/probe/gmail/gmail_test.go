package gmail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeQueriesThreadByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/users/me/threads/FMfcgzGabc123") {
			t.Errorf("path = %q, want the per-thread path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// Two messages; the newer internalDate wins.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]any{
				{"internalDate": "1721000000000"},
				{"internalDate": "1721466000000"},
			},
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGmail, Record: "FMfcgzGabc123"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	r := out["gmail:FMfcgzGabc123"]
	if r.LastActivity == nil {
		t.Fatal("no last activity for the gmail thread")
	}
	if !r.LastActivity.Equal(time.UnixMilli(1721466000000).UTC()) {
		t.Errorf("last activity = %v, want the newest message time", r.LastActivity)
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGmail, Record: "FMfcgzGabc123"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["gmail:FMfcgzGabc123"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}

// A thread the API cannot return (404) is unchecked, never a false unchanged.
func TestProbeNotFoundUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGmail, Record: "FMfcgzGmissing"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	r := out["gmail:FMfcgzGmissing"]
	if !r.Unchecked || r.LastActivity != nil {
		t.Errorf("result = %+v, want unchecked with no last activity", r)
	}
}
