package tui

// Tier is the gating tier of a keyword, from references/lexicon.md.
type Tier int

const (
	TierNav        Tier = iota // read-only or navigation, no write
	TierInternal               // reversible write, runs on the keypress
	TierCompletion             // completes or closes a task, confirm per task
	TierOutward                // sends outward, never sent from the TUI (queued)
)

// ArgStyle is how a verb's typed argument maps onto its macro script's flags,
// derived from the scripts' own usage (references/lexicon.md and the script
// headers). Keeping it explicit data, rather than inferring from the script
// name, makes the mapping unit-testable and obvious at the call site.
type ArgStyle int

const (
	ArgNone       ArgStyle = iota // nav and outward verbs take no macro argument
	ArgReason                     // --reason <text> (escalate, done, drop)
	ArgEntry                      // --entry <text> (log, link, fixref via td_worklog.sh)
	ArgPositional                 // one positional after the ref (defer date, column, priority)
	ArgDraft                      // --text <text> (td_draft.sh; channel and recipient are a follow-up)
	ArgMerge                      // --survivor <ref> --loser <ref>... with no positional ref
)

// Verb is one lexicon keyword bound to a key.
type Verb struct {
	Name     string
	Key      rune
	Tier     Tier
	NeedsArg bool     // opens the argument prompt before acting
	Script   string   // macro script under the scripts dir, empty for nav/outward
	Prompt   string   // prompt label shown when NeedsArg
	Arg      ArgStyle // how the typed argument maps onto the script's flags
}

// verbs is the keymap. Every writing keyword maps to its deterministic macro
// script (references/lexicon.md). Keys are single, distinct runes; the nav tier
// covers movement and read-only actions. Outward verbs (nudge, email, teams)
// carry no key here: they are reached through draft-then-send and are queued,
// never sent, so they are looked up by name.
var verbs = []Verb{
	// Internal (immediate, reversible)
	{Name: "log", Key: 'l', Tier: TierInternal, NeedsArg: true, Script: "td_worklog.sh", Prompt: "log note", Arg: ArgEntry},
	{Name: "link", Key: 'L', Tier: TierInternal, NeedsArg: true, Script: "td_worklog.sh", Prompt: "link url [label]", Arg: ArgEntry},
	{Name: "defer", Key: 'd', Tier: TierInternal, NeedsArg: true, Script: "td_defer.sh", Prompt: "defer when", Arg: ArgPositional},
	{Name: "col", Key: 'c', Tier: TierInternal, NeedsArg: true, Script: "td_move.sh", Prompt: "column", Arg: ArgPositional},
	{Name: "prio", Key: 'p', Tier: TierInternal, NeedsArg: true, Script: "td_reprioritize.sh", Prompt: "priority p1-p4", Arg: ArgPositional},
	{Name: "fixref", Key: 'f', Tier: TierInternal, NeedsArg: true, Script: "td_worklog.sh", Prompt: "correction", Arg: ArgEntry},
	{Name: "escalate", Key: 'e', Tier: TierInternal, NeedsArg: true, Script: "td_escalate.sh", Prompt: "escalate reason", Arg: ArgReason},
	{Name: "draft", Key: 'r', Tier: TierInternal, NeedsArg: true, Script: "td_draft.sh", Prompt: "draft channel text", Arg: ArgDraft},

	// Completion (confirm per task)
	{Name: "done", Key: 'D', Tier: TierCompletion, NeedsArg: true, Script: "td_complete.sh", Prompt: "done reason", Arg: ArgReason},
	{Name: "drop", Key: 'X', Tier: TierCompletion, NeedsArg: true, Script: "td_drop.sh", Prompt: "drop reason", Arg: ArgReason},
	{Name: "merge", Key: 'M', Tier: TierCompletion, NeedsArg: true, Script: "td_merge.sh", Prompt: "merge survivor loser...", Arg: ArgMerge},

	// Navigation and read-only
	{Name: "dig", Key: 'g', Tier: TierNav, NeedsArg: false},
	{Name: "more", Key: 'm', Tier: TierNav, NeedsArg: false},
	{Name: "open", Key: 'o', Tier: TierNav, NeedsArg: false},
	{Name: "next", Key: 'n', Tier: TierNav, NeedsArg: false},
	{Name: "skip", Key: 's', Tier: TierNav, NeedsArg: false},
	{Name: "back", Key: 'b', Tier: TierNav, NeedsArg: false},
	{Name: "quit", Key: 'q', Tier: TierNav, NeedsArg: false},
	{Name: "help", Key: '?', Tier: TierNav, NeedsArg: false},

	// Outward (no key; reached via draft-then-send, always queued)
	{Name: "nudge", Tier: TierOutward},
	{Name: "email", Tier: TierOutward},
	{Name: "teams", Tier: TierOutward},
}

// VerbForKey resolves a key to its verb. A key of 0 (the outward verbs) never
// resolves, so outward sends cannot be triggered by a stray keypress.
func VerbForKey(r rune) (Verb, bool) {
	for _, v := range verbs {
		if v.Key != 0 && v.Key == r {
			return v, true
		}
	}
	return Verb{}, false
}

// VerbForName resolves a verb by name.
func VerbForName(name string) (Verb, bool) {
	for _, v := range verbs {
		if v.Name == name {
			return v, true
		}
	}
	return Verb{}, false
}

// Verbs returns the keymap in display order, for the help overlay and the action
// line legend.
func Verbs() []Verb { return verbs }
