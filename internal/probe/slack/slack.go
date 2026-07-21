// Package slack probes Slack thread freshness with one channel history call per
// channel, fetching replies only for threads whose latest_reply advanced.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

const defaultBaseURL = "https://slack.com/api"

// Client is the Slack freshness prober.
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

// WithHTTPClient overrides the http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Slack prober. The limiter is sized to Slack's Tier 3 ceiling of
// roughly 50 requests per minute.
func New(token string, opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
		limiter: rate.NewLimiter(rate.Every(time.Minute/50), 5),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// System identifies this prober.
func (c *Client) System() links.System { return links.SystemSlack }

// parseSlackTS converts a Slack ts string ("1699999999.000100") to a time.
func parseSlackTS(ts string) (time.Time, error) {
	secStr := ts
	if dot := strings.IndexByte(ts, '.'); dot >= 0 {
		secStr = ts[:dot]
	}
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing slack ts %q: %w", ts, err)
	}
	return time.Unix(sec, 0).UTC(), nil
}

// slackResponse is the subset of a Slack API envelope this prober decodes.
type slackResponse struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error"`
	Messages []slackMessage `json:"messages"`
}

type slackMessage struct {
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	ReplyCount  int    `json:"reply_count"`
	LatestReply string `json:"latest_reply"`
}

// Probe groups links by channel, fetches one history per channel, and fetches
// replies only for threads whose latest_reply advanced past the watermark. Any
// failure makes every link unchecked with the fitting reason.
func (c *Client) Probe(ctx context.Context, ls []links.Link, since sources.Watermark) (map[string]probe.Result, error) {
	out := make(map[string]probe.Result, len(ls))

	uncheckAll := func(reason probe.Reason) map[string]probe.Result {
		for _, l := range ls {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: reason}
		}
		return out
	}

	// Group links by channel.
	byChannel := map[string][]links.Link{}
	for _, l := range ls {
		ch := l.Fields["channel"]
		byChannel[ch] = append(byChannel[ch], l)
	}

	for channel, chLinks := range byChannel {
		hist, err := c.call(ctx, "conversations.history", url.Values{"channel": {channel}})
		if err != nil {
			return uncheckAll(reasonFor(err)), nil
		}

		// Index thread parents by their ts, remembering the latest reply.
		latest := map[string]string{}
		for _, m := range hist.Messages {
			key := m.ThreadTS
			if key == "" {
				key = m.TS
			}
			lr := m.LatestReply
			if lr == "" {
				lr = m.TS
			}
			latest[key] = lr
		}

		for _, l := range chLinks {
			thread := l.Fields["thread"]
			lr, ok := latest[thread]
			if !ok {
				// The thread was not in the recent history window; treat its
				// last activity as the thread ts itself.
				lr = thread
			}

			lrTime, err := parseSlackTS(lr)
			if err != nil {
				out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
				continue
			}

			prev, seen := since[l.Key()]
			if seen && !lrTime.After(prev) {
				// Not advanced; the history call already told us the freshness.
				la := lrTime
				out[l.Key()] = probe.Result{LastActivity: &la}
				continue
			}

			// Advanced (or first sight): one replies call confirms the time.
			rep, err := c.call(ctx, "conversations.replies", url.Values{"channel": {channel}, "ts": {thread}})
			if err != nil {
				return uncheckAll(reasonFor(err)), nil
			}
			la := lrTime
			if n := len(rep.Messages); n > 0 {
				if t, err := parseSlackTS(rep.Messages[n-1].TS); err == nil {
					la = t
				}
			}
			out[l.Key()] = probe.Result{LastActivity: &la}
		}
	}

	return out, nil
}

// authError marks a Slack ok:false auth failure so Probe can map it to ReasonAuth.
type authError struct{ code string }

func (e authError) Error() string { return "slack auth: " + e.code }

// reasonFor maps a call error to the unchecked reason it should render.
func reasonFor(err error) probe.Reason {
	var ae authError
	if errors.As(err, &ae) {
		return probe.ReasonAuth
	}
	return probe.ReasonError
}

// call performs one Slack API GET, enforces the rate limit, and decodes the
// envelope. An ok:false with an auth error becomes an authError.
func (c *Client) call(ctx context.Context, method string, q url.Values) (slackResponse, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return slackResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+method+"?"+q.Encode(), nil)
	if err != nil {
		return slackResponse{}, fmt.Errorf("building %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return slackResponse{}, fmt.Errorf("calling %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return slackResponse{}, fmt.Errorf("slack %s returned %s", method, resp.Status)
	}

	var out slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return slackResponse{}, fmt.Errorf("decoding %s: %w", method, err)
	}
	if !out.OK {
		switch out.Error {
		case "invalid_auth", "token_revoked", "account_inactive":
			return slackResponse{}, authError{code: out.Error}
		default:
			return slackResponse{}, fmt.Errorf("slack %s: %s", method, out.Error)
		}
	}
	return out, nil
}
