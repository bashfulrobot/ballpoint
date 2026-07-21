package tui

import "testing"

func TestVerbForKey(t *testing.T) {
	v, ok := VerbForKey('l')
	if !ok || v.Name != "log" || v.Tier != TierInternal || !v.NeedsArg {
		t.Fatalf("key l = %+v ok=%v, want log/internal/needs-arg", v, ok)
	}
	v, ok = VerbForKey('n')
	if !ok || v.Name != "next" || v.NeedsArg {
		t.Fatalf("key n = %+v, want next with no arg", v)
	}
	if _, ok := VerbForKey('Z'); ok {
		t.Error("unknown key should not resolve")
	}
}

func TestVerbTiers(t *testing.T) {
	done, ok := VerbForKey('D')
	if !ok || done.Name != "done" || done.Tier != TierCompletion {
		t.Errorf("D = %+v ok=%v, want done/completion", done, ok)
	}
	nudge, ok := VerbForName("nudge")
	if !ok || nudge.Tier != TierOutward {
		t.Errorf("nudge = %+v ok=%v, want outward", nudge, ok)
	}
}

// An outward verb has no key, so a stray keypress can never fire an outward send.
func TestOutwardVerbsHaveNoKey(t *testing.T) {
	for _, name := range []string{"nudge", "email", "teams"} {
		v, _ := VerbForName(name)
		if v.Key != 0 {
			t.Errorf("outward verb %q has key %q, want none", name, v.Key)
		}
	}
	if _, ok := VerbForKey(0); ok {
		t.Error("the zero rune must not resolve to a verb")
	}
}

// Every writing verb maps to a macro script; nav and outward verbs do not.
func TestScriptPresenceByTier(t *testing.T) {
	for _, v := range Verbs() {
		switch v.Tier {
		case TierInternal, TierCompletion:
			if v.Script == "" {
				t.Errorf("verb %q (writing tier) has no script", v.Name)
			}
		case TierNav, TierOutward:
			if v.Script != "" {
				t.Errorf("verb %q (%v) should carry no script, got %q", v.Name, v.Tier, v.Script)
			}
		}
	}
}

func TestKeysUnique(t *testing.T) {
	seen := map[rune]string{}
	for _, v := range Verbs() {
		if v.Key == 0 {
			continue
		}
		if prev, ok := seen[v.Key]; ok {
			t.Errorf("key %q bound to both %q and %q", v.Key, prev, v.Name)
		}
		seen[v.Key] = v.Name
	}
}
