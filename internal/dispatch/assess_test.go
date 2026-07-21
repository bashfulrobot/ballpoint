package dispatch

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseAssessmentPlainJSON(t *testing.T) {
	raw := `{"summary":"PR merged","verb":"note","links":[{"label":"PR 42","url":"https://x/42"}],"next":"close it"}`
	a, err := ParseAssessment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "PR merged" || a.Verb != "note" || a.Next != "close it" {
		t.Errorf("assessment = %+v", a)
	}
	if len(a.Links) != 1 || a.Links[0].Label != "PR 42" || a.Links[0].URL != "https://x/42" {
		t.Errorf("links = %+v", a.Links)
	}
}

func TestParseAssessmentStripsFences(t *testing.T) {
	raw := "```json\n{\"summary\":\"ok\"}\n```"
	a, err := ParseAssessment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "ok" {
		t.Errorf("summary = %q", a.Summary)
	}
}

func TestParseAssessmentRejectsEmptySummary(t *testing.T) {
	if _, err := ParseAssessment(`{"summary":"  "}`); err == nil {
		t.Error("empty summary should be rejected")
	}
}

func TestParseAssessmentRejectsGarbage(t *testing.T) {
	if _, err := ParseAssessment("not json at all"); err == nil {
		t.Error("non-JSON should be rejected")
	}
}

func TestDecodeCLIResultDetectsUsageLimit(t *testing.T) {
	status := 429
	env := cliResult{IsError: true, APIErrorStatus: &status, Result: "rate limited"}
	if _, _, err := assessmentFromEnvelope(env); !errors.Is(err, ErrUsageLimit) {
		t.Errorf("err = %v, want ErrUsageLimit", err)
	}
}

func TestDecodeCLIResultOtherError(t *testing.T) {
	env := cliResult{IsError: true, Result: "model exploded"}
	_, _, err := assessmentFromEnvelope(env)
	if err == nil || errors.Is(err, ErrUsageLimit) {
		t.Errorf("err = %v, want a non-usage error", err)
	}
}

// The error text carries model-derived Result, which surfaces on --status, so
// control bytes in it must be stripped before they can reach the terminal.
func TestDecodeCLIResultErrorSanitizesResult(t *testing.T) {
	env := cliResult{IsError: true, Result: "boom\x1b[2Jcleared"}
	_, _, err := assessmentFromEnvelope(env)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("error kept a control byte: %q", err)
	}
}

func TestParseAssessmentExtractsFromProse(t *testing.T) {
	raw := "Sure, here is the assessment:\n{\"summary\":\"looks done\"}\nLet me know if you need more."
	a, err := ParseAssessment(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "looks done" {
		t.Errorf("summary = %q", a.Summary)
	}
}

func TestExtractJSONObjectIgnoresBracesInStrings(t *testing.T) {
	got := extractJSONObject(`prefix {"summary":"a } b","next":""} suffix`)
	if got != `{"summary":"a } b","next":""}` {
		t.Errorf("extractJSONObject = %q", got)
	}
}

func TestDecodeCLIOutputUsageLimitOnNonzeroExit(t *testing.T) {
	// claude exits nonzero on an API error but still writes the envelope to
	// stdout. The envelope must be parsed before the exec error is trusted, or
	// the 429 requeue path never fires.
	status := 429
	env := cliResult{IsError: true, APIErrorStatus: &status, Result: "rate limited"}
	out, _ := json.Marshal(env)
	_, _, err := decodeCLIOutput(out, errors.New("exit status 1"))
	if !errors.Is(err, ErrUsageLimit) {
		t.Errorf("err = %v, want ErrUsageLimit", err)
	}
}

func TestDecodeCLIOutputExecErrorWithoutEnvelope(t *testing.T) {
	_, _, err := decodeCLIOutput([]byte("segfault"), errors.New("exit status 139"))
	if err == nil || errors.Is(err, ErrUsageLimit) {
		t.Errorf("err = %v, want a plain run error", err)
	}
}

func TestDecodeCLIOutputSuccess(t *testing.T) {
	env := cliResult{Result: `{"summary":"done"}`, TotalCostUSD: 0.03}
	out, _ := json.Marshal(env)
	a, cost, err := decodeCLIOutput(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary != "done" || cost != 0.03 {
		t.Errorf("assessment = %+v cost = %v", a, cost)
	}
}

func TestClaudeArgvIsLockedDown(t *testing.T) {
	argv := claudeArgv("haiku")
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"-p", "--output-format json", "--model haiku",
		"--permission-mode dontAsk", "--no-session-persistence",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("claude argv %q missing %q", joined, want)
		}
	}
	// The tools flag must be present with an empty value (no tools).
	found := false
	for i, a := range argv {
		if a == "--tools" && i+1 < len(argv) && argv[i+1] == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("claude argv must pass --tools \"\": %v", argv)
	}
}
