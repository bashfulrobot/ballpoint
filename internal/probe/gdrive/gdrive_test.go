package gdrive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestProbeParsesModifiedTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"files": []map[string]any{
				{"id": "1AbC_dEF", "modifiedTime": "2026-07-20T09:00:00Z"},
			},
		})
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
