// Package probeset is the composition root that wires each source's prober into
// a registry. It sits above the engine and the probers so the engine package
// stays free of any dependency on a concrete prober.
package probeset

import (
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/probe/aha"
	"github.com/bashfulrobot/ballpoint/internal/probe/gdrive"
	"github.com/bashfulrobot/ballpoint/internal/probe/gmail"
	"github.com/bashfulrobot/ballpoint/internal/probe/salesforce"
	"github.com/bashfulrobot/ballpoint/internal/probe/slack"
)

// Credentials holds each source's token. An empty field means that source has
// no credential, so its prober is not registered and its links render
// unchecked. Values are never logged.
type Credentials struct {
	Slack  string
	Aha    string
	Google string // shared by Gmail and Drive
	// Salesforce is true when the sf CLI is available. Salesforce auth lives in
	// the CLI's own store, not this off-store secrets file, so there is no token.
	Salesforce bool
}

// Build registers a prober for each system whose credential is present.
func Build(c Credentials) *probe.Registry {
	reg := &probe.Registry{}
	if c.Slack != "" {
		reg.Register(slack.New(c.Slack))
	}
	if c.Aha != "" {
		reg.Register(aha.New(c.Aha))
	}
	if c.Google != "" {
		reg.Register(gmail.New(c.Google))
		reg.Register(gdrive.New(c.Google))
	}
	if c.Salesforce {
		reg.Register(salesforce.New())
	}
	return reg
}
