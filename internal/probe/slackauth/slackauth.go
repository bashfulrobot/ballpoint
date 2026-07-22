// Package slackauth resolves Slack xoxc/xoxd credential pairs from the
// slack-token-refresh store (~/.config/slack/credentials.json). Ballpoint reuses
// the browser-session tokens that tool already refreshes rather than a static
// secret, the same way the Gmail and Drive probers defer to the gws CLI. Slack
// rejects an xoxc token unless the request also carries the matching d cookie,
// so both halves are resolved together. No credential value is ever logged or
// placed in an error; failures name the stage, not the secret.
package slackauth

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Creds is one workspace's browser-session credential pair. Token is the xoxc
// bearer token; Cookie is the xoxd value sent as the d cookie.
type Creds struct {
	Token  string
	Cookie string
}

// Store holds the resolvable credentials from the slack-token-refresh file. It
// indexes each workspace by the host of its stored url so a Slack link's host
// selects the right pair, and remembers a single workspace as a fallback for the
// common one-workspace setup where the link host may not match the stored url
// exactly.
type Store struct {
	byHost map[string]Creds
	single *Creds
}

// DefaultPath returns the slack-token-refresh credentials path, honoring
// XDG_CONFIG_HOME and falling back to ~/.config.
func DefaultPath() (string, error) {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(root, "slack", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", "slack", "credentials.json"), nil
}

// credentialsFile is the subset of the slack-token-refresh file this package
// decodes. Shape: {workspaces: {<name>: {xoxc, xoxd, url}}}.
type credentialsFile struct {
	Workspaces map[string]struct {
		XOXC string `json:"xoxc"`
		XOXD string `json:"xoxd"`
		URL  string `json:"url"`
	} `json:"workspaces"`
}

// Load reads and parses the credentials file. A missing file is not an error: it
// returns an empty Store so Slack simply renders unchecked, which is the normal
// state on a host that has never run slack-token-refresh. A malformed file
// returns a generic error that never echoes the file's bytes, since those bytes
// carry tokens.
func Load(path string) (*Store, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{}, nil
		}
		return nil, fmt.Errorf("reading slack credentials: %w", err)
	}

	var parsed credentialsFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// Do not wrap the decoder error: a json syntax error can echo a byte of
		// the credential blob, and this message may reach a stderr warning.
		return nil, fmt.Errorf("slack credentials malformed")
	}

	s := &Store{byHost: map[string]Creds{}}
	for _, w := range parsed.Workspaces {
		if w.XOXC == "" || w.XOXD == "" {
			continue
		}
		c := Creds{Token: w.XOXC, Cookie: w.XOXD}
		if host := hostOf(w.URL); host != "" {
			s.byHost[host] = c
		}
		// Remember the last complete workspace; promoted to the fallback below
		// only when exactly one complete workspace exists.
		last := c
		s.single = &last
	}
	// The single-workspace fallback is only unambiguous with one workspace. With
	// more than one, a host that matches none must stay unresolved rather than
	// guess.
	if completeCount(parsed) != 1 {
		s.single = nil
	}
	return s, nil
}

// completeCount counts workspaces carrying both halves of the credential.
func completeCount(f credentialsFile) int {
	n := 0
	for _, w := range f.Workspaces {
		if w.XOXC != "" && w.XOXD != "" {
			n++
		}
	}
	return n
}

// ForHost returns the credential pair for a Slack link's host (for example
// "kong.slack.com"). It matches the host against each workspace's stored url,
// then falls back to the sole workspace when only one is configured. It is
// nil-safe so an empty Store from a missing file simply reports no match.
func (s *Store) ForHost(host string) (Creds, bool) {
	if s == nil {
		return Creds{}, false
	}
	if c, ok := s.byHost[strings.ToLower(host)]; ok {
		return c, true
	}
	if s.single != nil {
		return *s.single, true
	}
	return Creds{}, false
}

// Empty reports whether the Store can resolve any credential. The composition
// root uses it to skip registering the Slack prober when there is nothing to
// resolve, so Slack renders unchecked without a failed lookup per link.
func (s *Store) Empty() bool {
	return s == nil || (len(s.byHost) == 0 && s.single == nil)
}

// hostOf returns the lowercased host of a URL, or "" if it does not parse to one.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}
