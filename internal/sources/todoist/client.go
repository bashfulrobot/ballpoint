// Package todoist is the direct Todoist v1 HTTP client. It implements
// sources.Source, replacing the shelled-out td CLI on the read path.
package todoist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const defaultBaseURL = "https://api.todoist.com/api/v1"

// Client talks to the Todoist v1 API. Construct it with New.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	http      *http.Client
	limit     int
	limiter   *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base, used by tests to point at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithVersion sets the version reported in the User-Agent.
func WithVersion(v string) Option {
	return func(c *Client) {
		c.userAgent = "ballpoint/" + v + " (+https://github.com/bashfulrobot/ballpoint)"
	}
}

// WithConcurrency sets the bounded fetch limit. Values below 1 are ignored.
func WithConcurrency(n int) Option {
	return func(c *Client) {
		if n >= 1 {
			c.limit = n
		}
	}
}

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithRateLimit sets the client-side request ceiling in requests per second
// with the given burst. A non-positive perSecond disables limiting, which the
// benchmark uses to measure concurrency alone.
func WithRateLimit(perSecond float64, burst int) Option {
	return func(c *Client) {
		if perSecond <= 0 {
			c.limiter = rate.NewLimiter(rate.Inf, 0)
			return
		}
		if burst < 1 {
			burst = 1
		}
		c.limiter = rate.NewLimiter(rate.Limit(perSecond), burst)
	}
}

// New builds a Client. Concurrency defaults to 12. A client-side rate limiter
// (default 20 requests per second, burst 20) is a second guard so a large
// scope cannot burst without bound. The exact Todoist ceiling is not pinned
// here; callers tune it with WithRateLimit, and the live run in issue #3 is
// where the real limit gets confirmed.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL:   defaultBaseURL,
		token:     token,
		userAgent: "ballpoint/dev (+https://github.com/bashfulrobot/ballpoint)",
		http:      &http.Client{Timeout: 30 * time.Second},
		limit:     12,
		limiter:   rate.NewLimiter(rate.Limit(20), 20),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name identifies this source.
func (c *Client) Name() string { return "todoist" }

// page is the envelope every list endpoint returns.
type page struct {
	Results    json.RawMessage `json:"results"`
	NextCursor string          `json:"next_cursor"`
}

// getAll drains a paginated list endpoint into out, which must be a pointer to
// a slice, following next_cursor until it is empty. query holds any endpoint
// specific parameters; cursor and limit are added here.
func (c *Client) getAll(ctx context.Context, path string, query url.Values, out any) error {
	// Accumulate the raw result arrays, then unmarshal once into out.
	var buf []json.RawMessage

	cursor := ""
	for {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		q := url.Values{}
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		q.Set("limit", "200")
		if cursor != "" {
			q.Set("cursor", cursor)
		}

		var p page
		if err := c.get(ctx, path, q, &p); err != nil {
			return err
		}

		var items []json.RawMessage
		if err := json.Unmarshal(p.Results, &items); err != nil {
			return fmt.Errorf("decoding %s results: %w", path, err)
		}
		buf = append(buf, items...)

		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}

	combined, err := json.Marshal(buf)
	if err != nil {
		return fmt.Errorf("recombining %s results: %w", path, err)
	}
	if err := json.Unmarshal(combined, out); err != nil {
		return fmt.Errorf("decoding %s into target: %w", path, err)
	}
	return nil
}

// get performs one GET and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("building request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("requesting %s: %w", path, err)
	}
	// The body is drained by the JSON decoder below; a close error on a
	// fully read response carries no signal the caller can act on.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The token is in a header, never the URL, so naming the endpoint and
		// status leaks nothing.
		return fmt.Errorf("todoist %s returned %s", path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s response: %w", path, err)
	}
	return nil
}
