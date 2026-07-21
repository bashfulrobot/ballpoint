// Package salesforce probes Salesforce record freshness through the already
// authenticated `sf` CLI. Records are grouped by sObject, one `sf data query`
// runs per group, and each record's LastModifiedDate becomes its last activity
// time. Auth lives entirely in the CLI's own store, so this package holds no
// token. Any CLI, status, or query failure renders the affected links unchecked,
// never a false unchanged.
package salesforce

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// sfTimeLayout is Salesforce's LastModifiedDate format, e.g.
// 2026-07-20T09:00:00.000+0000.
const sfTimeLayout = "2006-01-02T15:04:05.000-0700"

// prefixObject maps a Salesforce 3-char id key prefix to its sObject, used when
// a record arrives without an object hint from its URL.
var prefixObject = map[string]string{
	"001": "Account", "003": "Contact", "005": "User",
	"006": "Opportunity", "00Q": "Lead", "500": "Case",
	"701": "Campaign", "800": "Contract",
}

var (
	// sfIDCharset admits only a bare 15 or 18 char Salesforce id.
	sfIDCharset = regexp.MustCompile(`^[0-9A-Za-z]{15,18}$`)
	// sfCaseCharset admits only a numeric Case number.
	sfCaseCharset = regexp.MustCompile(`^[0-9]+$`)
	// sfObjectCharset admits only a valid sObject API name.
	sfObjectCharset = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
)

// Runner runs a command and returns its stdout. `sf ... --json` writes its
// envelope to stdout even on a non-zero exit, so callers parse stdout rather
// than relying on the error alone.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client is the Salesforce freshness prober.
type Client struct {
	runner  Runner
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithRunner injects a command runner, so tests drive the prober with recorded
// envelopes and no CLI.
func WithRunner(r Runner) Option { return func(c *Client) { c.runner = r } }

// New builds a Salesforce prober. The default runner shells out to `sf` and the
// limiter is a conservative 5 queries per second, one query per sObject group.
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

// Available reports whether the `sf` binary is on PATH, so the composition root
// registers this prober only when it can actually run.
func Available() bool {
	_, err := exec.LookPath("sf")
	return err == nil
}

// defaultRunner runs `sf` and captures stdout, returning it alongside any exit
// error so the caller can still parse the JSON envelope on a non-zero status.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.Bytes(), err
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemSalesforce }

// sfEnvelope is the subset of an `sf ... --json` response this prober decodes.
// status is 0 on success and non-zero on failure, where name and message carry
// the error.
type sfEnvelope struct {
	Status  int    `json:"status"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Result  struct {
		Records []sfRecord `json:"records"`
	} `json:"result"`
}

type sfRecord struct {
	Attributes struct {
		Type string `json:"type"`
	} `json:"attributes"`
	ID               string `json:"Id"`
	CaseNumber       string `json:"CaseNumber"`
	LastModifiedDate string `json:"LastModifiedDate"`
}

// Probe types each link to its sObject, groups id lookups by object and Case
// numbers on their own, and runs one query per group. The incoming watermark is
// unused: each query returns an absolute LastModifiedDate and the engine
// compares it against the work-log baseline.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))

	idsByObject := map[string][]links.Link{} // object -> id links
	var caseNumbers []links.Link             // all-digit Case numbers

	for _, l := range ls {
		rec := l.Record
		switch {
		case rec == "":
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
		case sfCaseCharset.MatchString(rec):
			caseNumbers = append(caseNumbers, l)
		case !sfIDCharset.MatchString(rec):
			// The record reached us but is neither a clean id nor a Case number,
			// so it cannot safely enter a SOQL string.
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
		default:
			object, ok := objectFor(l)
			switch {
			case !ok:
				out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonNoProbe}
			case !sfObjectCharset.MatchString(object):
				out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			default:
				idsByObject[object] = append(idsByObject[object], l)
			}
		}
	}

	for object, group := range idsByObject {
		c.queryIDs(ctx, object, group, out)
	}
	if len(caseNumbers) > 0 {
		c.queryCases(ctx, caseNumbers, out)
	}
	return out, nil
}

// objectFor resolves a record's sObject: the Lightning URL object hint when
// present, otherwise the id's 3-char key prefix. ok is false for an unmapped
// prefix, which renders the link unchecked with the no-probe reason.
func objectFor(l links.Link) (string, bool) {
	if object := l.Fields["object"]; object != "" {
		return object, true
	}
	object, ok := prefixObject[l.Record[:3]]
	return object, ok
}

// queryIDs runs one `SELECT Id, LastModifiedDate FROM <object> WHERE Id IN (...)`
// and maps each returned record back to its link by id.
func (c *Client) queryIDs(ctx context.Context, object string, group []links.Link, out map[string]probe.Result) {
	ids := make([]string, len(group))
	for i, l := range group {
		ids[i] = l.Record
	}
	soql := "SELECT Id, LastModifiedDate FROM " + object + " WHERE Id IN (" + quoteList(ids) + ")"

	env, reason, ok := c.runQuery(ctx, soql)
	if !ok {
		markGroup(group, reason, out)
		return
	}
	byID := map[string]sfRecord{}
	for _, r := range env.Result.Records {
		byID[r.ID] = r
	}
	for _, l := range group {
		r, seen := byID[l.Record]
		if !seen {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			continue
		}
		out[l.Key()] = parseActivity(r.LastModifiedDate)
	}
}

// queryCases runs one `SELECT CaseNumber, LastModifiedDate FROM Case WHERE
// CaseNumber IN (...)` and maps each returned record back to its link by number.
func (c *Client) queryCases(ctx context.Context, group []links.Link, out map[string]probe.Result) {
	nums := make([]string, len(group))
	for i, l := range group {
		nums[i] = l.Record
	}
	soql := "SELECT CaseNumber, LastModifiedDate FROM Case WHERE CaseNumber IN (" + quoteList(nums) + ")"

	env, reason, ok := c.runQuery(ctx, soql)
	if !ok {
		markGroup(group, reason, out)
		return
	}
	byNum := map[string]sfRecord{}
	for _, r := range env.Result.Records {
		byNum[r.CaseNumber] = r
	}
	for _, l := range group {
		r, seen := byNum[l.Record]
		if !seen {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonError}
			continue
		}
		out[l.Key()] = parseActivity(r.LastModifiedDate)
	}
}

// parseActivity parses a Salesforce timestamp, unchecked when it will not parse.
func parseActivity(raw string) probe.Result {
	t, err := time.Parse(sfTimeLayout, raw)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &t}
}

// markGroup renders every link in a failed query's group unchecked.
func markGroup(group []links.Link, reason probe.Reason, out map[string]probe.Result) {
	for _, l := range group {
		out[l.Key()] = probe.Result{Unchecked: true, Reason: reason}
	}
}

// runQuery runs one SOQL through the CLI and classifies the outcome. ok is false
// on any failure, with the reason to render. `sf --json` writes its envelope even
// on a non-zero exit, so the envelope status is authoritative over the exit code.
func (c *Client) runQuery(ctx context.Context, soql string) (sfEnvelope, probe.Reason, bool) {
	if err := c.limiter.Wait(ctx); err != nil {
		return sfEnvelope{}, probe.ReasonFromCtx(ctx), false
	}
	stdout, runErr := c.runner(ctx, "sf", "data", "query", "--query", soql, "--json")

	var env sfEnvelope
	if err := json.NewDecoder(io.LimitReader(bytes.NewReader(stdout), probe.MaxResponseBytes)).Decode(&env); err != nil {
		// No parseable envelope: the binary failed to run or produced garbage. The
		// run error is folded into the context classification below.
		_ = runErr
		return sfEnvelope{}, probe.ReasonFromCtx(ctx), false
	}
	if env.Status != 0 {
		if isAuthFailure(env.Name, env.Message) {
			return env, probe.ReasonAuth, false
		}
		return env, probe.ReasonError, false
	}
	return env, "", true
}

// isAuthFailure reports whether an `sf` error names a missing or expired org
// authorization rather than a query problem.
func isAuthFailure(name, message string) bool {
	hay := strings.ToLower(name + " " + message)
	for _, kw := range []string{"authorization", "authenticate", "no org", "not been authorized", "expired", "session", "logged in", "login"} {
		if strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}

// quoteList renders ids as a SOQL value list: 'a','b'. Every id is charset
// validated before it reaches this point, so no escaping is required.
func quoteList(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "'" + id + "'"
	}
	return strings.Join(quoted, ",")
}
