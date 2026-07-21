package aha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeQueriesIdeaByReference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth = %q, want Bearer test-token", got)
		}
		// The reference is an idea key, so the prober must hit the ideas
		// endpoint, not features.
		if !strings.HasSuffix(r.URL.Path, "/ideas/GTWY-I-1484") {
			t.Errorf("path = %q, want the per-idea path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"idea": map[string]any{"reference_num": "GTWY-I-1484", "updated_at": "2026-07-20T09:00:00Z"},
		})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemAha, Record: "GTWY-I-1484"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["aha:GTWY-I-1484"]; r.LastActivity == nil {
		t.Fatalf("no last activity for the aha record")
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemAha, Record: "GTWY-I-1484"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["aha:GTWY-I-1484"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}

// A record the API cannot return (404) is unchecked, never a false unchanged.
// This is the false-negative path the engine exists to prevent.
func TestProbeNotFoundUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemAha, Record: "GTWY-I-9999"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	r := out["aha:GTWY-I-9999"]
	if !r.Unchecked {
		t.Errorf("result = %+v, want unchecked for a record the API did not return", r)
	}
	if r.LastActivity != nil {
		t.Error("a missing record must not carry a last activity time")
	}
}
