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
	case ArgLink:
		return linkArgv(ref, text)
	case ArgMerge:
		return mergeArgv(ref, text)
	default:
		return []string{ref}
	}
}

// linkArgv maps "url [label]" onto td_worklog.sh's structured --link flag, so a
// link is recorded as a link rather than free text in the entry. td_worklog.sh
// requires --entry, so a short breadcrumb is written alongside the link.
func linkArgv(ref, text string) []string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return []string{ref, "--entry", "linked"}
	}
	url := fields[0]
	label := "link"
	if len(fields) > 1 {
		label = strings.Join(fields[1:], " ")
	}
	return []string{ref, "--link", label + "=" + url, "--entry", "linked " + label}
}

// mergeArgv maps "loser [loser...]" onto td_merge.sh's flags. The survivor is the
// current card's ref, not a retyped token, so the walk cannot merge into the
// wrong survivor by fat-finger. Every remaining field is a loser.
func mergeArgv(survivor, text string) []string {
	fields := strings.Fields(text)
	argv := make([]string, 0, len(fields)*2+4)
	argv = append(argv, "--survivor", survivor)
	for _, loser := range fields {
		argv = append(argv, "--loser", loser)
	}
	return append(argv, "--reason", "merged during triage walk")
}
