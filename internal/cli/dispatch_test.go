package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseDispatchFlagsDefaults(t *testing.T) {
	f, helped, err := parseDispatchFlags(nil, &bytes.Buffer{})
	if err != nil || helped {
		t.Fatalf("parse: helped=%v err=%v", helped, err)
	}
	if f.concurrency != 2 {
		t.Errorf("concurrency default = %d, want 2", f.concurrency)
	}
	if f.model != "haiku" {
		t.Errorf("model default = %q, want haiku", f.model)
	}
}

func TestParseDispatchFlagsRejectsPositional(t *testing.T) {
	if _, _, err := parseDispatchFlags([]string{"extra"}, &bytes.Buffer{}); err == nil {
		t.Error("positional arg should be rejected")
	}
}

func TestParseDispatchFlagsParsesAll(t *testing.T) {
	f, _, err := parseDispatchFlags([]string{"--concurrency", "4", "--model", "sonnet", "--dry-run", "--status", "--scripts-dir", "/s"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if f.concurrency != 4 || f.model != "sonnet" || !f.dryRun || !f.status || f.scriptsDir != "/s" {
		t.Errorf("flags = %+v", f)
	}
}

func TestRunDispatchStatusEmpty(t *testing.T) {
	// With no dispatch dir, --status prints a "no jobs" line and returns nil.
	var out bytes.Buffer
	deps := dispatchDeps{root: t.TempDir(), statusOnly: true}
	if err := runDispatch(deps, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no dispatch jobs") {
		t.Errorf("status output = %q", out.String())
	}
}
