package probeset

import (
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
)

// Build registers a prober only for a system whose credential is present. A
// missing credential leaves that system unregistered, so the engine renders it
// unchecked rather than failing the run.
func TestBuildSkipsMissingCreds(t *testing.T) {
	reg := Build(Credentials{Slack: "test-token"}) // aha, google absent

	if _, ok := reg.For(links.SystemSlack); !ok {
		t.Error("slack prober not registered despite a token")
	}
	if _, ok := reg.For(links.SystemAha); ok {
		t.Error("aha prober registered without a token")
	}
	if _, ok := reg.For(links.SystemGmail); ok {
		t.Error("gmail prober registered without a google token")
	}
}

// The Salesforce prober registers on the availability flag, not a token: its
// auth lives in the sf CLI's own store.
func TestBuildSalesforceOnAvailability(t *testing.T) {
	reg := Build(Credentials{Salesforce: true})
	if _, ok := reg.For(links.SystemSalesforce); !ok {
		t.Error("salesforce prober not registered despite Salesforce=true")
	}

	reg2 := Build(Credentials{})
	if _, ok := reg2.For(links.SystemSalesforce); ok {
		t.Error("salesforce prober registered when Salesforce=false")
	}
}

// A google token registers both Gmail and Drive, since they share it.
func TestBuildGoogleSharesToken(t *testing.T) {
	reg := Build(Credentials{Google: "test-token"})
	if _, ok := reg.For(links.SystemGmail); !ok {
		t.Error("gmail not registered with a google token")
	}
	if _, ok := reg.For(links.SystemGDrive); !ok {
		t.Error("gdrive not registered with a google token")
	}
}
