package gmail

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

func TestProbeParsesHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"threads": []map[string]any{
				{"id": "FMfcgzGabc123", "internalDate": "1721466000000"},
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
	if r := out["gmail:FMfcgzGabc123"]; r.LastActivity == nil {
		t.Fatal("no last activity for the gmail thread")
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
