package slackauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCreds writes a credentials file into a temp dir and returns its path.
func writeCreds(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing creds: %v", err)
	}
	return path
}

func TestDefaultPathHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join("/xdg", "slack", "credentials.json"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestDefaultPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join(".config", "slack", "credentials.json")) {
		t.Errorf("path = %q, want it to end in .config/slack/credentials.json", got)
	}
}

// A missing file is the normal state on a host that never ran the refresher. It
// must be an empty Store, not an error, so Slack simply renders unchecked.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load missing = %v, want nil", err)
	}
	if !s.Empty() {
		t.Error("missing file should yield an empty store")
	}
	if _, ok := s.ForHost("kong.slack.com"); ok {
		t.Error("empty store resolved a host")
	}
}

func TestLoadSingleWorkspaceMatchesHostAndFallsBack(t *testing.T) {
	path := writeCreds(t, `{"workspaces":{"kong":{"xoxc":"xoxc-1","xoxd":"xoxd-1","url":"https://kong.slack.com"}}}`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, ok := s.ForHost("kong.slack.com")
	if !ok || c.Token != "xoxc-1" || c.Cookie != "xoxd-1" {
		t.Errorf("ForHost(kong) = %+v, %v; want the kong pair", c, ok)
	}
	// With a single workspace, an unmatched host falls back to it.
	if c, ok := s.ForHost("other.slack.com"); !ok || c.Token != "xoxc-1" {
		t.Errorf("single-workspace fallback failed: %+v, %v", c, ok)
	}
}

func TestLoadMultipleWorkspacesMatchByHostNoFallback(t *testing.T) {
	path := writeCreds(t, `{"workspaces":{
		"kong":{"xoxc":"xoxc-k","xoxd":"xoxd-k","url":"https://kong.slack.com"},
		"acme":{"xoxc":"xoxc-a","xoxd":"xoxd-a","url":"https://acme.slack.com"}}}`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c, ok := s.ForHost("acme.slack.com"); !ok || c.Token != "xoxc-a" || c.Cookie != "xoxd-a" {
		t.Errorf("ForHost(acme) = %+v, %v; want the acme pair", c, ok)
	}
	// With two workspaces, a host that matches neither must not guess.
	if _, ok := s.ForHost("unknown.slack.com"); ok {
		t.Error("multi-workspace store guessed for an unmatched host")
	}
}

// A workspace missing either half of the pair is unusable and skipped.
func TestLoadSkipsIncompleteWorkspace(t *testing.T) {
	path := writeCreds(t, `{"workspaces":{"half":{"xoxc":"xoxc-only","url":"https://half.slack.com"}}}`)
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.Empty() {
		t.Error("a workspace missing its cookie should leave the store empty")
	}
}

// A malformed file must fail without echoing any byte of the credential blob,
// since the offending bytes carry tokens.
func TestLoadMalformedNeverLeaks(t *testing.T) {
	path := writeCreds(t, `{"workspaces":{"kong":{"xoxc":"xoxc-SENTINEL" oops`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for malformed credentials")
	}
	if strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("error leaked credential bytes: %v", err)
	}
}
