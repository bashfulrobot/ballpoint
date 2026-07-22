// Package gwsauth mints a short-lived Google OAuth2 access token from the
// already authenticated `gws` (Google Workspace CLI). It reads the CLI's stored
// authorized_user credentials via `gws auth export --unmasked` and exchanges the
// refresh token for a fresh access token, so ballpoint never persists a Google
// token in its own secrets file. Auth lives entirely in the gws store, the same
// way the Salesforce prober defers to the `sf` CLI. No credential value is ever
// logged or placed in an error; failures report the stage, not the secret.
package gwsauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// defaultTokenURL is Google's OAuth2 token endpoint. Overridable in tests.
const defaultTokenURL = "https://oauth2.googleapis.com/token"

// lookPath is indirected so tests can drive Available without depending on PATH.
var lookPath = exec.LookPath

// Runner runs a command and returns its stdout. Injected so tests drive the
// exchange with a canned credential blob and never shell out to gws.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Source turns the gws-stored credentials into a fresh access token.
type Source struct {
	runner   Runner
	tokenURL string
	http     *http.Client
}

// Option configures a Source.
type Option func(*Source)

// WithRunner injects a command runner.
func WithRunner(r Runner) Option { return func(s *Source) { s.runner = r } }

// WithTokenURL points the exchange at a mock token endpoint.
func WithTokenURL(u string) Option { return func(s *Source) { s.tokenURL = u } }

// New builds a Source. The default runner shells out to gws and the HTTP client
// carries a short timeout, since the exchange is a single request.
func New(opts ...Option) *Source {
	s := &Source{
		runner:   defaultRunner,
		tokenURL: defaultTokenURL,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Available reports whether the `gws` binary is on PATH, so the composition root
// only attempts an exchange when the CLI could actually run. Whether gws is
// authenticated is deferred to AccessToken, which fails cleanly when the binary
// is present but not logged in.
func Available() bool {
	_, err := lookPath("gws")
	return err == nil
}

// defaultRunner runs a command and captures stdout.
func defaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.Bytes(), err
}

// authorizedUser is the subset of `gws auth export` this package decodes. It is
// Google's standard authorized_user credential shape.
type authorizedUser struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// AccessToken returns a fresh Google access token. It reads the gws-stored
// credentials, then exchanges the refresh token for an access token.
func (s *Source) AccessToken(ctx context.Context) (string, error) {
	out, err := s.runner(ctx, "gws", "auth", "export", "--unmasked")
	if err != nil {
		return "", fmt.Errorf("gws auth export: %w", err)
	}
	var cred authorizedUser
	if err := json.Unmarshal(out, &cred); err != nil {
		return "", fmt.Errorf("decoding gws credentials: %w", err)
	}
	if cred.ClientID == "" || cred.ClientSecret == "" || cred.RefreshToken == "" {
		return "", fmt.Errorf("gws credentials incomplete")
	}
	return s.exchange(ctx, cred)
}

// exchange performs the OAuth2 refresh-token grant. The client secret and
// refresh token travel only in the POST body, never in an error or a log line.
func (s *Source) exchange(ctx context.Context, cred authorizedUser) (string, error) {
	form := url.Values{
		"client_id":     {cred.ClientID},
		"client_secret": {cred.ClientSecret},
		"refresh_token": {cred.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token exchange: status %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned no access token")
	}
	return body.AccessToken, nil
}
