package gdrive

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

func TestProbeQueriesFileByID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/files/1AbC_dEF") {
			t.Errorf("path = %q, want the per-file path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"modifiedTime": "2026-07-20T09:00:00Z"})
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGDrive, Record: "1AbC_dEF"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["gdrive:1AbC_dEF"]; r.LastActivity == nil {
		t.Fatal("no last activity for the drive file")
	}
}

func TestProbeAuthUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGDrive, Record: "1AbC_dEF"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["gdrive:1AbC_dEF"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}

// A file the API cannot return (404) is unchecked, never a false unchanged.
func TestProbeNotFoundUnchecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New("test-token", WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemGDrive, Record: "1missing"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	r := out["gdrive:1missing"]
	if !r.Unchecked || r.LastActivity != nil {
		t.Errorf("result = %+v, want unchecked with no last activity", r)
	}
}
