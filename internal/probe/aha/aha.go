// Package aha probes Aha record freshness. It queries each linked record by its
// reference key and reports that record's absolute last-updated time, so a
// record the API cannot confirm renders unchecked rather than a false unchanged.
package aha

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Aha freshness prober.
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

// New builds an Aha prober with a conservative 5 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://api.aha.io/api/v1",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Limit(5), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemAha }

// ahaResponse is the subset of a single Aha feature response this prober
// decodes.
type ahaResponse struct {
	Feature struct {
		UpdatedAt string `json:"updated_at"`
	} `json:"feature"`
}

// Probe queries each linked record for its last-updated time. Any record it
// cannot positively confirm renders unchecked, never a false unchanged.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))
	for _, l := range ls {
		out[l.Key()] = c.probeOne(ctx, l)
	}
	return out, nil
}

// probeOne fetches one record's updated_at.
func (c *Client) probeOne(ctx context.Context, l links.Link) probe.Result {
	if l.Record == "" {
		return probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonFromCtx(ctx)}
	}

	target := c.baseURL + "/features/" + url.PathEscape(l.Record)
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

	var body ahaResponse
	if err := probe.DecodeJSON(resp.Body, &body); err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	t, err := time.Parse(time.RFC3339, body.Feature.UpdatedAt)
	if err != nil {
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &t}
}
