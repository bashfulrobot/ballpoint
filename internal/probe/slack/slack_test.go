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

// fixed returns a resolver that hands back one pair for any host, standing in
// for a loaded slack-token-refresh store.
func fixed(token, cookie string) Resolver {
	return func(string) (Creds, bool) { return Creds{Token: token, Cookie: cookie}, true }
}

// fakeSlack serves one channel with two threads. Thread A advanced past the
// watermark, thread B did not. It counts replies calls so the test can assert
// replies is fetched only for the advanced thread.
type fakeSlack struct{ repliesCalls int32 }

func (f *fakeSlack) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer xoxc-test" {
			t.Errorf("auth = %q, want Bearer xoxc-test", got)
		}
		if got := r.Header.Get("Cookie"); got != "d=xoxd-test" {
			t.Errorf("cookie = %q, want d=xoxd-test", got)
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

	c := New(fixed("xoxc-test", "xoxd-test"), WithBaseURL(srv.URL))

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

// A thread that scrolled out of the recent history window must still be
// confirmed against replies, so a fresh reply is never reported as a silent
// no-change and the watermark never regresses below the thread's real activity.
func TestProbeConfirmsOutOfWindowThread(t *testing.T) {
	var repliesCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			// The linked thread is not in this window.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1700500000.000000", "thread_ts": "1700500000.000000",
						"reply_count": 0, "latest_reply": "1700500000.000000"},
				},
			})
		case "/conversations.replies":
			atomic.AddInt32(&repliesCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"ts": "1699999999.000100"},
					{"ts": "1700100000.000000"}, // a reply newer than the watermark
				},
			})
		default:
			http.Error(w, r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(fixed("xoxc-test", "xoxd-test"), WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemSlack, Raw: "x", Record: "C1:1699999999.000100",
		Fields: map[string]string{"channel": "C1", "thread": "1699999999.000100"}}}

	// A prior watermark behind the real latest reply. The buggy path would read
	// the parent ts, skip replies, and regress the watermark.
	since := sources.Watermark{"slack:C1:1699999999.000100": mustTS("1700000500.000000")}

	out, err := c.Probe(context.Background(), ls, since)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if got := atomic.LoadInt32(&repliesCalls); got != 1 {
		t.Errorf("replies calls = %d, want 1 (out-of-window thread must be confirmed)", got)
	}
	r := out["slack:C1:1699999999.000100"]
	if r.Unchecked {
		t.Fatalf("out-of-window thread rendered unchecked: %+v", r)
	}
	if r.LastActivity == nil || !r.LastActivity.Equal(mustTS("1700100000.000000")) {
		t.Errorf("last activity = %v, want the confirmed reply time 1700100000", r.LastActivity)
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

	c := New(fixed("xoxc-test", "xoxd-test"), WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemSlack, Record: "C1:1", Fields: map[string]string{"channel": "C1", "thread": "1"}}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["slack:C1:1"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}

// Real permalinks from two workspaces run through the host resolver: the matched
// workspace's credentials reach the API, and a link whose host has no workspace
// is unchecked with ReasonAuth without borrowing the other workspace's creds.
func TestProbeResolvesCredsByLinkHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the kong workspace resolves, so only its bearer may reach the API.
		if got := r.Header.Get("Authorization"); got != "Bearer xoxc-kong" {
			t.Errorf("auth = %q, want Bearer xoxc-kong", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"ts": "1699999999.000100", "thread_ts": "1699999999.000100", "latest_reply": "1699999999.000100"},
			},
		})
	}))
	defer srv.Close()

	resolve := func(host string) (Creds, bool) {
		if host == "kong.slack.com" {
			return Creds{Token: "xoxc-kong", Cookie: "xoxd-kong"}, true
		}
		return Creds{}, false
	}
	c := New(resolve, WithBaseURL(srv.URL))

	ls := []links.Link{
		{System: links.SystemSlack, Raw: "https://kong.slack.com/archives/C1/p1699999999000100",
			Record: "C1:1699999999.000100", Fields: map[string]string{"channel": "C1", "thread": "1699999999.000100"}},
		{System: links.SystemSlack, Raw: "https://acme.slack.com/archives/C2/p1699999999000100",
			Record: "C2:1699999999.000100", Fields: map[string]string{"channel": "C2", "thread": "1699999999.000100"}},
	}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["slack:C1:1699999999.000100"]; r.Unchecked {
		t.Errorf("kong link unexpectedly unchecked: %+v", r)
	}
	if r := out["slack:C2:1699999999.000100"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("acme link = %+v, want unchecked with ReasonAuth", r)
	}
}

// When the resolver has no workspace for a link's host, the channel cannot be
// authenticated, so every link is unchecked with ReasonAuth and no API call is
// made.
func TestProbeNoCredsForHostUnchecked(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	noCreds := func(string) (Creds, bool) { return Creds{}, false }
	c := New(noCreds, WithBaseURL(srv.URL))
	ls := []links.Link{{System: links.SystemSlack, Raw: "https://unknown.slack.com/archives/C1/p1",
		Record: "C1:1", Fields: map[string]string{"channel": "C1", "thread": "1"}}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if called {
		t.Error("Slack API called despite no resolvable credentials")
	}
	if r := out["slack:C1:1"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked with ReasonAuth", r)
	}
}
