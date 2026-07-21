package links

import "testing"

func TestLinkKey(t *testing.T) {
	l := Link{System: SystemSlack, Record: "C1:1699999999.000100"}
	if got, want := l.Key(), "slack:C1:1699999999.000100"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}

// A link whose record could not be parsed has an empty record, so its key is
// just the system with a trailing colon. The engine treats an empty record as
// unparseable rather than a real identity.
func TestLinkKeyEmptyRecord(t *testing.T) {
	l := Link{System: SystemURL, Record: ""}
	if got, want := l.Key(), "url:"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
}
