// Package github probes GitHub issue, pull request, and commit freshness through
// the already authenticated `gh` CLI. One `gh api` call per item returns its last
// update time. Auth lives entirely in the CLI's own store, so this package holds
// no token. Any CLI, auth, or parse failure renders the affected link unchecked,
// never a false unchanged.
package github

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// maxProbes caps how many `gh api` calls one run will make. Each link costs one
// call, and task text is attacker-influenceable, so without a cap a comment
// stuffed with hundreds of distinct GitHub URLs could spawn hundreds of gh
// subprocesses and burn the API rate limit. The cap sits far above any real
// corpus; the excess renders unchecked rather than spawning.
const maxProbes = 50

// Record-part charsets, re-validated here as defense in depth. A link's record
// and fields are read from an on-disk cache that another process could tamper,
// so every part is re-checked before it is spliced into a gh api path.
var (
	ghOwnerCharset  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*)$`)
	ghRepoCharset   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	ghNumberCharset = regexp.MustCompile(`^[0-9]+$`)
	ghShaCharset    = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
)

// endpointForKind maps a parsed link kind to its REST collection path segment.
var endpointForKind = map[string]string{
	"issue":  "issues",
	"pull":   "pulls",
	"commit": "commits",
}

// Runner runs a command and returns its stdout on success. On a non-zero exit it
// returns the combined stdout and stderr so the caller can classify the failure
// (an auth error's hint is on stderr), alongside the exec error.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client is the GitHub freshness prober.
type Client struct {
	runner  Runner
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithRunner injects a command runner, so tests drive the prober with recorded
// responses and no CLI.
func WithRunner(r Runner) Option { return func(c *Client) { c.runner = r } }

// WithLimiter injects a rate limiter, so a test that exercises many calls can
// pass a permissive one instead of waiting on the conservative default.
func WithLimiter(l *rate.Limiter) Option { return func(c *Client) { c.limiter = l } }

// New builds a GitHub prober. The default runner shells out to `gh` and the
// limiter is a conservative 5 calls per second.
func New(opts ...Option) *Client {
	c := &Client{
		runner:  defaultRunner,
		limiter: rate.NewLimiter(rate.Limit(5), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Available reports whether the `gh` binary is on PATH, so the composition root
// registers this prober only when it can actually run.
func Available() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// defaultRunner runs `gh` and captures stdout, returning the combined output
// alongside the exit error so the caller can classify an auth failure from the
// hint gh writes to stderr.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return append(stdout.Bytes(), stderr.Bytes()...), err
	}
	return stdout.Bytes(), nil
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemGitHub }

// ghItem is the subset of a gh api response this prober decodes. updated_at is
// present on an issue or pull response; commit is present on a commit response,
// whose last activity is the committer date.
type ghItem struct {
	UpdatedAt string `json:"updated_at"`
	Commit    struct {
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// Probe fetches each link's last update time with one `gh api` call per link.
// The incoming watermark is unused: each call returns an absolute timestamp and
// the engine compares it against the work-log baseline.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))

	var valid []links.Link
	for _, l := range ls {
		if reason, bad := invalid(l); bad {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: reason}
			continue
		}
		valid = append(valid, l)
	}
	// Sort so the cap is deterministic. Links past the cap render unchecked
	// instead of spawning another subprocess.
	sort.Slice(valid, func(i, j int) bool { return valid[i].Key() < valid[j].Key() })
	for i, l := range valid {
		if i >= maxProbes {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonTooMany}
			continue
		}
		out[l.Key()] = c.probeOne(ctx, l)
	}
	return out, nil
}

// invalid reports the reason a link cannot be probed, if any. An empty record is
// unparseable; a record present but with a field that fails its charset is an
// error, so a crafted owner, repo, kind, or id never reaches a gh api path.
func invalid(l links.Link) (probe.Reason, bool) {
	if l.Record == "" {
		return probe.ReasonUnparseable, true
	}
	owner, repo, kind, id := l.Fields["owner"], l.Fields["repo"], l.Fields["kind"], l.Fields["id"]
	if _, ok := endpointForKind[kind]; !ok {
		return probe.ReasonError, true
	}
	if !ghOwnerCharset.MatchString(owner) || !ghRepoCharset.MatchString(repo) {
		return probe.ReasonError, true
	}
	if kind == "commit" {
		if !ghShaCharset.MatchString(id) {
			return probe.ReasonError, true
		}
	} else if !ghNumberCharset.MatchString(id) {
		return probe.ReasonError, true
	}
	return "", false
}

// probeOne runs one `gh api repos/<owner>/<repo>/<endpoint>/<id>` and maps the
// response timestamp to a result.
func (c *Client) probeOne(ctx context.Context, l links.Link) probe.Result {
	if err := c.limiter.Wait(ctx); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonFromCtx(ctx)}
	}
	owner, repo, kind, id := l.Fields["owner"], l.Fields["repo"], l.Fields["kind"], l.Fields["id"]
	path := "repos/" + owner + "/" + repo + "/" + endpointForKind[kind] + "/" + id

	stdout, runErr := c.runner(ctx, "gh", "api", path)
	if runErr != nil {
		if ctx.Err() != nil {
			return probe.Result{Unchecked: true, Reason: probe.ReasonFromCtx(ctx)}
		}
		if isAuthFailure(stdout) {
			return probe.Result{Unchecked: true, Reason: probe.ReasonAuth}
		}
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}

	var item ghItem
	if err := probe.DecodeJSON(bytes.NewReader(stdout), &item); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	raw := item.UpdatedAt
	if kind == "commit" {
		raw = item.Commit.Committer.Date
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &t}
}

// authMarkers are the substrings gh emits on an authentication failure, whether
// the request returned HTTP 401 or gh refused to run because no token is
// configured. Matching keeps the ReasonAuth versus ReasonError label
// trustworthy; a generic API or network failure stays ReasonError.
var authMarkers = []string{
	"http 401",
	"bad credentials",
	"requires authentication",
	"authentication required",
	"gh auth login",
}

// isAuthFailure reports whether gh's failure output names an auth problem.
func isAuthFailure(output []byte) bool {
	s := strings.ToLower(string(output))
	for _, m := range authMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}
