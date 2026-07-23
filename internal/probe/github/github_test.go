package github

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// recordRunner returns a canned response and records the exact args it received.
func recordRunner(out string, gotArgs *[]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*gotArgs = append([]string{name}, args...)
		return []byte(out), nil
	}
}

func ghLink(owner, repo, kind, id string) links.Link {
	return links.Link{
		System: links.SystemGitHub,
		Record: owner + "/" + repo + "/" + kind + "/" + id,
		Fields: map[string]string{"owner": owner, "repo": repo, "kind": kind, "id": id},
	}
}

const okIssue = `{"updated_at":"2026-07-20T09:00:00Z"}`

// An issue link runs `gh api repos/<owner>/<repo>/issues/<n>` and its updated_at
// becomes the last activity time.
func TestProbeIssue(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(okIssue, &args)))
	l := ghLink("bashfulrobot", "ballpoint", "issue", "45")

	out, err := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out[l.Key()]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	want := []string{"gh", "api", "repos/bashfulrobot/ballpoint/issues/45"}
	if fmt.Sprint(args) != fmt.Sprint(want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

// A pull request link resolves through the pulls endpoint.
func TestProbePull(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(`{"updated_at":"2026-07-20T09:00:00Z"}`, &args)))
	l := ghLink("bashfulrobot", "ballpoint", "pull", "12")

	out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	if fmt.Sprint(args) != fmt.Sprint([]string{"gh", "api", "repos/bashfulrobot/ballpoint/pulls/12"}) {
		t.Errorf("args = %v, want the pulls endpoint", args)
	}
}

// A commit link resolves through the commits endpoint, and its last activity is
// the committer date, not a top-level updated_at (a commit has none).
func TestProbeCommitCommitterDate(t *testing.T) {
	var args []string
	env := `{"commit":{"committer":{"date":"2026-07-20T09:00:00Z"}}}`
	c := New(WithRunner(recordRunner(env, &args)))
	l := ghLink("bashfulrobot", "ballpoint", "commit", "abcdef0123456789")

	out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time from the committer date", r)
	}
	if fmt.Sprint(args) != fmt.Sprint([]string{"gh", "api", "repos/bashfulrobot/ballpoint/commits/abcdef0123456789"}) {
		t.Errorf("args = %v, want the commits endpoint", args)
	}
}

// An auth failure output renders ReasonAuth; a generic failure renders
// ReasonError. The reason label has to stay trustworthy.
func TestProbeAuthVsError(t *testing.T) {
	authRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("gh: Bad credentials (HTTP 401)"), errors.New("exit status 1")
	}
	c := New(WithRunner(authRunner))
	l := ghLink("bashfulrobot", "ballpoint", "issue", "45")
	out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked ReasonAuth", r)
	}

	errRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"message":"Not Found","status":"404"}`), errors.New("exit status 1")
	}
	c2 := New(WithRunner(errRunner))
	out2, _ := c2.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out2[l.Key()]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("result = %+v, want unchecked ReasonError for a non-auth failure", r)
	}
}

// A record whose fields fail validation never reaches the runner. This is the
// injection guard against a tampered on-disk cache. The dot-segment cases (".",
// "..") are the traversal guard: the repo charset admits dot, so these have to be
// rejected explicitly or they would build a path like repos/o/../issues/45.
func TestProbeInjectionGuard(t *testing.T) {
	cases := []struct {
		name            string
		owner, repo, id string
		kind            string
	}{
		{"repo with slash", "o", "r/../secrets", "issue", "45"},
		{"repo is dotdot", "o", "..", "issue", "45"},
		{"repo is dot", "o", ".", "issue", "45"},
		{"owner leading dash", "-o", "r", "issue", "45"},
		{"id not a number", "o", "r", "issue", "45x"},
		{"commit id not hex", "o", "r", "commit", "nothex"},
		{"unknown kind", "o", "r", "wiki", "45"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				called = true
				return []byte(okIssue), nil
			}))
			l := links.Link{
				System: links.SystemGitHub,
				Record: tc.owner + "/" + tc.repo + "/" + tc.kind + "/" + tc.id,
				Fields: map[string]string{"owner": tc.owner, "repo": tc.repo, "kind": tc.kind, "id": tc.id},
			}
			out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
			if r := out[l.Key()]; !r.Unchecked || r.Reason != probe.ReasonError {
				t.Errorf("result = %+v, want unchecked ReasonError", r)
			}
			if called {
				t.Error("runner was called with an unvalidated record; the guard must run first")
			}
		})
	}
}

// A cancelled context renders unchecked (a timeout reason), never a false
// unchanged, and the limiter wait is where the cancellation is caught.
func TestProbeContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte(okIssue), nil
	}))
	l := ghLink("bashfulrobot", "ballpoint", "issue", "45")
	out, _ := c.Probe(ctx, []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; !r.Unchecked || r.Reason != probe.ReasonTimeout {
		t.Errorf("result = %+v, want unchecked ReasonTimeout on a cancelled context", r)
	}
	if called {
		t.Error("runner ran despite a cancelled context; the limiter wait must short-circuit")
	}
}

// An empty record renders unchecked with the unparseable reason and never
// reaches the runner.
func TestProbeEmptyRecordUnparseable(t *testing.T) {
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}))
	l := links.Link{System: links.SystemGitHub, Raw: "https://github.com/o/r"}
	out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; !r.Unchecked || r.Reason != probe.ReasonUnparseable {
		t.Errorf("result = %+v, want unchecked ReasonUnparseable", r)
	}
	if called {
		t.Error("runner was called for an empty record")
	}
}

// A response with no parseable timestamp renders unchecked, never a false
// unchanged.
func TestProbeMissingTimestampUnchecked(t *testing.T) {
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"updated_at":""}`), nil
	}))
	l := ghLink("bashfulrobot", "ballpoint", "issue", "45")
	out, _ := c.Probe(context.Background(), []links.Link{l}, sources.Watermark{})
	if r := out[l.Key()]; !r.Unchecked {
		t.Errorf("result = %+v, want unchecked for a missing timestamp", r)
	}
}

// More links than the probe cap: the excess renders unchecked (ReasonTooMany)
// instead of each spawning a subprocess.
func TestProbeCapsCallCount(t *testing.T) {
	calls := 0
	c := New(
		WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			calls++
			return []byte(okIssue), nil
		}),
		WithLimiter(rate.NewLimiter(rate.Inf, 1)),
	)
	var ls []links.Link
	for i := range maxProbes + 5 {
		ls = append(ls, ghLink("bashfulrobot", "ballpoint", "issue", fmt.Sprintf("%d", i)))
	}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if calls > maxProbes {
		t.Errorf("runner called %d times, want at most %d (cap)", calls, maxProbes)
	}
	tooMany := 0
	for _, r := range out {
		if r.Reason == probe.ReasonTooMany {
			tooMany++
		}
	}
	if tooMany != 5 {
		t.Errorf("ReasonTooMany count = %d, want 5 (the excess links)", tooMany)
	}
}

// cappedBuffer keeps at most probe.MaxResponseBytes and reports every write as
// fully accepted, so a runaway gh cannot exhaust memory yet is never blocked.
func TestCappedBufferBoundsMemory(t *testing.T) {
	var b cappedBuffer
	chunk := make([]byte, 1<<20) // 1 MiB
	total := 0
	for range (probe.MaxResponseBytes / len(chunk)) + 8 { // overshoot the cap
		n, err := b.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("Write returned (%d, %v), want (%d, nil)", n, err, len(chunk))
		}
		total += n
	}
	if total <= probe.MaxResponseBytes {
		t.Fatalf("test did not overshoot the cap: wrote %d, cap %d", total, probe.MaxResponseBytes)
	}
	if got := len(b.Bytes()); got != probe.MaxResponseBytes {
		t.Errorf("buffered %d bytes, want the cap %d", got, probe.MaxResponseBytes)
	}
}
