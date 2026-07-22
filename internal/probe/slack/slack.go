// Package slack probes Slack thread freshness with one channel history call per
// channel, fetching replies only for threads whose latest_reply advanced.
package slack

import (
	"context"
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

// Creds is one workspace's browser-session credential pair. Token is the xoxc
// bearer token; Cookie is the xoxd value sent as the d cookie. Slack rejects an
// xoxc token unless the request also carries the matching d cookie, so both
// travel together on every call.
type Creds struct {
	Token  string
	Cookie string
}

// Resolver returns the credential pair for a Slack link's host (for example
// "kong.slack.com"), or false when no workspace matches. A channel belongs to
// exactly one workspace, so the prober resolves once per channel from the host
// of a link in that channel.
type Resolver func(host string) (Creds, bool)

// Client is the Slack freshness prober.
type Client struct {
	baseURL string
	resolve Resolver
	http    *http.Client
	limiter *rate.Limiter
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at a mock server.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Slack prober. The resolver supplies per-workspace credentials,
// sourced from the slack-token-refresh store rather than a static token. The
// limiter is sized to Slack's Tier 3 ceiling of roughly 50 requests per minute.
func New(resolve Resolver, opts ...Option) *Client {
	c := &Client{
		baseURL: defaultBaseURL,
		resolve: resolve,
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

// credsFor resolves the workspace credentials for a channel from the host of one
// of its links. Every link in a channel shares a workspace, so the first is
// representative.
func (c *Client) credsFor(chLinks []links.Link) (Creds, bool) {
	if c.resolve == nil || len(chLinks) == 0 {
		return Creds{}, false
	}
	return c.resolve(hostOf(chLinks[0].Raw))
}

// hostOf returns the lowercased host of a Slack permalink, or "" if it does not
// parse to one.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

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

	// Group links by channel. A link that did not parse to a channel and thread
	// is unchecked directly, so one bad link never becomes a channel="" API
	// call that would poison the whole batch.
	byChannel := map[string][]links.Link{}
	for _, l := range ls {
		ch, thread := l.Fields["channel"], l.Fields["thread"]
		if ch == "" || thread == "" {
			out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
			continue
		}
		byChannel[ch] = append(byChannel[ch], l)
	}

	for channel, chLinks := range byChannel {
		// A channel belongs to one workspace, so resolve credentials once from a
		// link's host. No workspace match means we cannot authenticate to this
		// channel, so every link in it is unchecked with the auth reason rather
		// than a silent no-change.
		creds, ok := c.credsFor(chLinks)
		if !ok {
			for _, l := range chLinks {
				out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonAuth}
			}
			continue
		}

		hist, err := c.call(ctx, creds, "conversations.history", url.Values{"channel": {channel}})
		if err != nil {
			for _, l := range chLinks {
				out[l.Key()] = probe.Result{Unchecked: true, Reason: reasonFor(ctx, err)}
			}
			continue
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
			lr, inWindow := latest[thread]

			// A thread still inside the history window whose latest_reply has
			// not advanced past the watermark is answered from history alone,
			// no replies call. Every other case (advanced, first sight, or
			// scrolled out of the window) must be confirmed against replies, so
			// an old thread never reads as a silent no-change.
			if inWindow {
				lrTime, err := parseSlackTS(lr)
				if err != nil {
					out[l.Key()] = probe.Result{Unchecked: true, Reason: probe.ReasonUnparseable}
					continue
				}
				if prev, seen := since[l.Key()]; seen && !lrTime.After(prev) {
					la := lrTime
					out[l.Key()] = probe.Result{LastActivity: &la}
					continue
				}
			}

			out[l.Key()] = c.confirmThread(ctx, creds, channel, thread)
		}
	}

	return out, nil
}

// confirmThread fetches a thread's replies and reports the newest message time.
// A transport or auth failure, or a thread the API returns empty, is unchecked,
// never a false no-change.
func (c *Client) confirmThread(ctx context.Context, creds Creds, channel, thread string) probe.Result {
	rep, err := c.call(ctx, creds, "conversations.replies", url.Values{"channel": {channel}, "ts": {thread}})
	if err != nil {
		return probe.Result{Unchecked: true, Reason: reasonFor(ctx, err)}
	}
	// Take the newest of every message ts and every latest_reply. The parent
	// message carries latest_reply, which is authoritative even when the thread
	// has more replies than one page returns, so this does not underreport a
	// thread with a newer reply on a later page.
	var newest time.Time
	consider := func(ts string) {
		if ts == "" {
			return
		}
		if t, err := parseSlackTS(ts); err == nil && t.After(newest) {
			newest = t
		}
	}
	for _, m := range rep.Messages {
		consider(m.TS)
		consider(m.LatestReply)
	}
	if newest.IsZero() {
		// The thread carried no parseable message, so freshness is unconfirmed.
		return probe.Result{Unchecked: true, Reason: probe.ReasonError}
	}
	return probe.Result{LastActivity: &newest}
}

// authError marks a Slack ok:false auth failure so Probe can map it to ReasonAuth.
type authError struct{ code string }

func (e authError) Error() string { return "slack auth: " + e.code }

// reasonFor maps a call error to the unchecked reason it should render. An auth
// failure is ReasonAuth, a tripped context deadline is ReasonTimeout, and
// anything else is a generic probe error.
func reasonFor(ctx context.Context, err error) probe.Reason {
	var ae authError
	if errors.As(err, &ae) {
		return probe.ReasonAuth
	}
	if ctx.Err() != nil {
		return probe.ReasonTimeout
	}
	return probe.ReasonError
}

// call performs one Slack API GET, enforces the rate limit, and decodes the
// envelope. An ok:false with an auth error becomes an authError.
func (c *Client) call(ctx context.Context, creds Creds, method string, q url.Values) (slackResponse, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return slackResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+method+"?"+q.Encode(), nil)
	if err != nil {
		return slackResponse{}, fmt.Errorf("building %s request: %w", method, err)
	}
	// The xoxc token authenticates only alongside its d cookie; sending the
	// bearer without the cookie is rejected as invalid_auth.
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	req.Header.Set("Cookie", "d="+creds.Cookie)

	resp, err := c.http.Do(req)
	if err != nil {
		return slackResponse{}, fmt.Errorf("calling %s: %w", method, err)
	}
	defer probe.DrainClose(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return slackResponse{}, fmt.Errorf("slack %s returned %s", method, resp.Status)
	}

	var out slackResponse
	if err := probe.DecodeJSON(resp.Body, &out); err != nil {
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
