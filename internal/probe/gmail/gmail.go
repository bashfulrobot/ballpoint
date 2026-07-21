// Package gmail probes Gmail thread freshness with a single threads query.
package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// gmailResponse is the subset of the threads list this prober decodes.
// internalDate is epoch milliseconds as a string.
type gmailResponse struct {
	Threads []struct {
		ID           string `json:"id"`
		InternalDate string `json:"internalDate"`
	} `json:"threads"`
}

// Probe fetches recent threads and maps each requested thread id to its
// internalDate. A non-2xx makes every link unchecked.
func (c *Client) Probe(ctx context.Context, ls []links.Link, _ sources.Watermark) (map[string]probe.Result, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/users/me/threads", nil)
	if err != nil {
		return nil, fmt.Errorf("building gmail request: %w", err)
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

	var body gmailResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return uncheck(ls, probe.ReasonError), nil
	}

	updated := map[string]time.Time{}
	for _, th := range body.Threads {
		if ms, err := strconv.ParseInt(th.InternalDate, 10, 64); err == nil {
			updated[th.ID] = time.UnixMilli(ms).UTC()
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
