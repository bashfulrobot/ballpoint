// Package gdrive probes Drive file freshness with a single modifiedTime query.
package gdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Client is the Drive freshness prober.
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

// New builds a Drive prober with a conservative 10 request per second limiter.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: "https://www.googleapis.com/drive/v3",
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
func (c *Client) System() links.System { return links.SystemGDrive }

// driveResponse is the subset of the files list this prober decodes.
type driveResponse struct {
	Files []struct {
		ID           string `json:"id"`
		ModifiedTime string `json:"modifiedTime"`
	} `json:"files"`
}

// Probe fetches files with their modifiedTime and maps each requested file id
// to it. A non-2xx makes every link unchecked.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/files?fields=files(id,modifiedTime)", nil)
	if err != nil {
		return nil, fmt.Errorf("building drive request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return uncheck(ls, probe.ReasonAuth), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uncheck(ls, probe.ReasonError), nil
	}

	var body driveResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}

	updated := map[string]time.Time{}
	for _, f := range body.Files {
		if t, err := time.Parse(time.RFC3339, f.ModifiedTime); err == nil {
			updated[f.ID] = t
		}
	}

	out := make(map[string]probe.Result, len(ls))
	for _, l := range ls {
		if t, ok := updated[l.Record]; ok {
			tt := t
			out[l.Key()] = probe.Result{LastActivity: &tt}
		} else {
			out[l.Key()] = probe.Result{}
		}
	}
	return out, nil
}

// uncheck marks every link unchecked with a reason.
func uncheck(ls []links.Link, reason probe.Reason) map[string]probe.Result {
	out := make(map[string]probe.Result, len(ls))
	for _, l := range ls {
		out[l.Key()] = probe.Result{Unchecked: true, Reason: reason}
	}
	return out
}
