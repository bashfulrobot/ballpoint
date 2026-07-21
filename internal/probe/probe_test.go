package probe

import (
	"context"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

func TestRegistry(t *testing.T) {
	var r Registry
	stub := stubProber{system: links.SystemSlack}
	r.Register(stub)

	got, ok := r.For(links.SystemSlack)
	if !ok || got.System() != links.SystemSlack {
		t.Fatalf("For(slack) = %v, %v; want the registered prober", got, ok)
	}
	if _, ok := r.For(links.SystemGmail); ok {
		t.Error("For(gmail) ok = true, want false for an unregistered system")
	}
}

// stubProber is a minimal Prober for tests in this package.
type stubProber struct {
	system links.System
	result map[string]ProbeResult
	err    error
}

func (s stubProber) System() links.System { return s.system }

func (s stubProber) Probe(_ context.Context, _ []links.Link, _ sources.Watermark) (map[string]ProbeResult, error) {
	return s.result, s.err
}
