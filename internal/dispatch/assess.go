package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrUsageLimit is returned by an assessor when the subscription usage or rate
// limit is hit. The orchestrator backs off and requeues rather than retrying.
var ErrUsageLimit = errors.New("usage limit reached")

// Link is one external reference in an assessment, preserved as a Markdown
// link when written to the work log.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Assessment is the structured result a job parses from the model. Summary is
// required; the rest are optional.
type Assessment struct {
	Summary string `json:"summary"`
	Verb    string `json:"verb"`
	Links   []Link `json:"links"`
	Next    string `json:"next"`
}

// cliResult is the envelope `claude -p --output-format json` prints. Only the
// fields the dispatcher reads are modeled.
type cliResult struct {
	IsError        bool    `json:"is_error"`
	APIErrorStatus *int    `json:"api_error_status"`
	Result         string  `json:"result"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// ParseAssessment decodes the model's final text into an Assessment. It strips
// a wrapping Markdown code fence if present and requires a non-empty summary.
func ParseAssessment(raw string) (Assessment, error) {
	body := stripFence(strings.TrimSpace(raw))
	var a Assessment
	if err := jsonUnmarshalStrict(body, &a); err != nil {
		return Assessment{}, fmt.Errorf("parsing assessment: %w", err)
	}
	if strings.TrimSpace(a.Summary) == "" {
		return Assessment{}, errors.New("assessment has an empty summary")
	}
	return a, nil
}

// assessmentFromEnvelope turns a decoded CLI envelope into an Assessment,
// mapping a 429 to ErrUsageLimit and any other error flag to a plain error.
func assessmentFromEnvelope(env cliResult) (Assessment, float64, error) {
	if env.IsError {
		if env.APIErrorStatus != nil && *env.APIErrorStatus == 429 {
			return Assessment{}, env.TotalCostUSD, ErrUsageLimit
		}
		return Assessment{}, env.TotalCostUSD, fmt.Errorf("claude reported an error: %s", env.Result)
	}
	a, err := ParseAssessment(env.Result)
	if err != nil {
		return Assessment{}, env.TotalCostUSD, err
	}
	return a, env.TotalCostUSD, nil
}

// stripFence removes a single leading ```lang line and a trailing ``` line, so
// a model that fenced its JSON still parses.
func stripFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	lines = lines[1:] // drop opening ```lang
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// jsonUnmarshalStrict decodes exactly one JSON value from s and rejects
// trailing content, so "garbage after json" is an error rather than silently
// decoding the prefix.
func jsonUnmarshalStrict(s string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing content after JSON")
	}
	return nil
}

// claudeArgv is the locked-down headless invocation. No tools, no prompts, no
// session persistence, JSON output. The prompt is fed on stdin, never argv, so
// task content cannot land in the process table.
func claudeArgv(model string) []string {
	return []string{
		"-p",
		"--output-format", "json",
		"--model", model,
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--no-session-persistence",
	}
}

// ExecAssess returns an assessor that shells out to the local claude CLI. The
// returned function matches Config.Assess.
func ExecAssess(model string) func(ctx context.Context, prompt string) (Assessment, float64, error) {
	return func(ctx context.Context, prompt string) (Assessment, float64, error) {
		cmd := exec.CommandContext(ctx, "claude", claudeArgv(model)...)
		cmd.Stdin = strings.NewReader(prompt)
		out, err := cmd.Output()
		if err != nil {
			// A cancelled context surfaces here; report it so the job requeues.
			if ctx.Err() != nil {
				return Assessment{}, 0, ctx.Err()
			}
			return Assessment{}, 0, fmt.Errorf("running claude: %w", err)
		}
		var env cliResult
		if err := json.Unmarshal(out, &env); err != nil {
			return Assessment{}, 0, fmt.Errorf("decoding claude output: %w", err)
		}
		return assessmentFromEnvelope(env)
	}
}
