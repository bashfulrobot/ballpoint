package tui

import "strings"

// BuildArgv turns a verb, a task ref, and the typed argument text into the exact
// argv the macro script expects. The ref is the "id:<taskID>" form the scripts
// take (see the script headers). Most verbs take the ref first; merge is the
// exception (it uses --survivor/--loser and has no positional ref), so BuildArgv
// returns the full argv rather than assuming ref-first everywhere.
func BuildArgv(v Verb, ref, text string) []string {
	text = strings.TrimSpace(text)
	switch v.Arg {
	case ArgReason:
		return []string{ref, "--reason", text}
	case ArgEntry:
		return []string{ref, "--entry", text}
	case ArgPositional:
		return []string{ref, text}
	case ArgDraft:
		return []string{ref, "--text", text}
	case ArgMerge:
		return mergeArgv(text)
	default:
		return []string{ref}
	}
}

// mergeArgv maps "survivor loser [loser...]" onto td_merge.sh's flags. The first
// ref survives; the rest are losers. A short input still produces a well-formed
// argv so the script reports the mistake rather than the model guessing.
func mergeArgv(text string) []string {
	fields := strings.Fields(text)
	argv := make([]string, 0, len(fields)*2+2)
	if len(fields) > 0 {
		argv = append(argv, "--survivor", fields[0])
		for _, loser := range fields[1:] {
			argv = append(argv, "--loser", loser)
		}
	}
	return append(argv, "--reason", "merged during triage walk")
}
