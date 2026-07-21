// Package gmail probes Gmail thread freshness. It queries each linked thread by
// id and reports the newest message time, so a thread the API cannot confirm
// renders unchecked rather than a false unchanged.
package gmail

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Gmail freshness prober.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// New builds a Gmail prober with a conservative 10 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://gmail.googleapis.com/gmail/v1",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(10), 10),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemGmail }

// gmailThread is the subset of a single thread response this prober decodes.
// internalDate is epoch milliseconds as a string, one per message.
type gmailThread struct {
	Messages []struct {
		InternalDate string `json:"internalDate"`
	} `json:"messages"`
}

// Probe queries each linked thread for its newest message time. Any thread it
// cannot positively confirm renders unchecked, never a false unchanged. The
// incoming watermark is unused on purpose: each query returns an absolute time
// and the engine compares it against the work-log baseline, so the cost is one
// request per record, bounded by the engine's per-system cap, the run deadline,
// and this client's rate limiter.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))
	for _, l := range ls {
		out[l.Key()] = c.probeOne(ctx, l)
	}
	return out, nil
}

// probeOne fetches one thread and returns its newest message time.
func (c *Client) probeOne(ctx context.Context, l links.Link) probe.Result {
	if l.Record == "" {
		return probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonFromCtx(ctx)}
	}

	target := c.baseURL + "/users/me/threads/" + url.PathEscape(l.Record) + "?format=minimal"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonFromCtx(ctx)}
	}
	defer probe.DrainClose(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return probe.Result{Unchecked: true, Reason: probe.ReasonAuth}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}

	var body gmailThread
	if err := probe.DecodeJSON(resp.Body, &body); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}

	var newest time.Time
	for _, m := range body.Messages {
		ms, err := strconv.ParseInt(m.InternalDate, 10, 64)
		if err != nil {
			continue
		}
		if t := time.UnixMilli(ms).UTC(); t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		// No parseable message time, so freshness cannot be confirmed.
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &newest}
}
