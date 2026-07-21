// Package probe holds the batch-by-system freshness engine: it groups a task's
// links by system, calls one Prober per system, enforces the unchecked
// invariant, reconciles against each task's work log, and emits JSON.
package probe

import (
	"context"
	"time"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// Reason explains a non-changed, non-quiet outcome.
type Reason string

// The reasons a link can render unchecked.
const (
	ReasonNoProbe      Reason = "no probe available"
	ReasonNotProbeable Reason = "not probeable"
	ReasonAuth         Reason = "credentials missing or expired"
	ReasonError        Reason = "probe error"
	ReasonTimeout      Reason = "probe timed out"
	ReasonUnparseable  Reason = "link could not be parsed"
)

// ProbeResult is a prober's per-link finding: a last activity time, or an
// unchecked reason.
type ProbeResult struct {
	LastActivity *time.Time
	Unchecked    bool
	Reason       Reason
}

// Prober checks one system. It receives every link for its system across all
// tasks at once so it can batch, and returns a result per link key. The
// engine, not the prober, decides Changed and writes watermarks.
type Prober interface {
	System() links.System
	Probe(ctx context.Context, ls []links.Link, since sources.Watermark) (map[string]ProbeResult, error)
}

// Registry maps a system to its prober.
type Registry struct {
	probers map[links.System]Prober
}

// Register adds a prober, keyed by its System.
func (r *Registry) Register(p Prober) {
	if r.probers == nil {
		r.probers = map[links.System]Prober{}
	}
	r.probers[p.System()] = p
}

// For returns the prober for a system, if one is registered.
func (r *Registry) For(s links.System) (Prober, bool) {
	p, ok := r.probers[s]
	return p, ok
}
