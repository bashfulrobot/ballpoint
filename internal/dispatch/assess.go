package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/bashfulrobot/ballpoint/internal/sanitize"
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

// ParseAssessment decodes the model's assessment. It strips a wrapping code
// fence, extracts the first balanced JSON object (so a haiku that adds a
// sentence before or after the JSON still parses), and requires a non-empty
// summary.
func ParseAssessment(raw string) (Assessment, error) {
	body := extractJSONObject(stripFence(strings.TrimSpace(raw)))
	var a Assessment
	if err := jsonUnmarshalStrict(body, &a); err != nil {
		return Assessment{}, fmt.Errorf("parsing assessment: %w", err)
	}
	if strings.TrimSpace(a.Summary) == "" {
		return Assessment{}, errors.New("assessment has an empty summary")
	}
	return a, nil
}

// extractJSONObject returns the first balanced {...} object in s, ignoring
// braces inside JSON strings, or s unchanged when there is no opening brace.
// This tolerates a model that wraps its JSON in prose despite the instruction
// to emit bare JSON.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}

// assessmentFromEnvelope turns a decoded CLI envelope into an Assessment,
// mapping a 429 to ErrUsageLimit and any other error flag to a plain error.
func assessmentFromEnvelope(env cliResult) (Assessment, float64, error) {
	if env.IsError {
		if env.APIErrorStatus != nil && *env.APIErrorStatus == 429 {
			return Assessment{}, env.TotalCostUSD, ErrUsageLimit
		}
		return Assessment{}, env.TotalCostUSD, fmt.Errorf("claude reported an error: %s", sanitize.Line(env.Result))
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

// claudeArgv is the locked-down headless invocation. --bare makes the run
// hermetic: no hooks, no auto-memory, no CLAUDE.md auto-discovery, so the piped
// prompt is the whole input and cost does not balloon with repo context. No
// tools, no prompts, no session persistence, JSON output. The prompt is fed on
// stdin, never argv, so task content cannot land in the process table.
//
// The empty string passed to --tools is the documented "disable all tools"
// value, verified against `claude --help` on the Claude Code CLI (v2.1.216):
// `--tools <tools...>  ... Use "" to disable all tools, "default" to use all
// tools, or specify tool names`. This is the enforcement that keeps a worker
// from running Bash, touching files, or reaching the network, so the flag and
// its empty value are load-bearing, not cosmetic. If a future CLI changes this
// contract, the dispatcher's no-outward-capability guarantee changes with it.
func claudeArgv(model string) []string {
	return []string{
		"-p",
		"--bare",
		"--output-format", "json",
		"--model", model,
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--no-session-persistence",
	}
}

// decodeCLIOutput turns claude's stdout (and the exec error, if any) into an
// Assessment. A valid JSON envelope is authoritative even when the process
// exited nonzero: claude writes the error envelope to stdout and exits 1 on an
// API error, including a 429 usage limit, so the envelope must be parsed before
// the exec error is trusted. Otherwise the usage-limit path never fires.
func decodeCLIOutput(stdout []byte, runErr error) (Assessment, float64, error) {
	var env cliResult
	if err := json.Unmarshal(stdout, &env); err == nil {
		return assessmentFromEnvelope(env)
	}
	if runErr != nil {
		return Assessment{}, 0, fmt.Errorf("running claude: %w", runErr)
	}
	return Assessment{}, 0, fmt.Errorf("decoding claude output: %q", string(stdout))
}

// ExecAssess returns an assessor that shells out to the local claude CLI. The
// returned function matches Config.Assess.
func ExecAssess(model string) func(ctx context.Context, prompt string) (Assessment, float64, error) {
	return func(ctx context.Context, prompt string) (Assessment, float64, error) {
		cmd := exec.CommandContext(ctx, "claude", claudeArgv(model)...)
		cmd.Stdin = strings.NewReader(prompt)
		// Output captures stdout even on a nonzero exit; the error envelope
		// lives there, so it is decoded rather than discarded.
		out, err := cmd.Output()
		if ctx.Err() != nil {
			// A cancelled context (a peer job hit the usage limit, or Ctrl-C)
			// requeues rather than failing.
			return Assessment{}, 0, ctx.Err()
		}
		return decodeCLIOutput(out, err)
	}
}
