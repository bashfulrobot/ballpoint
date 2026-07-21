package salesforce

import (
	"context"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/links"
	"github.com/bashfulrobot/ballpoint/internal/probe"
	"github.com/bashfulrobot/ballpoint/internal/sources"
)

// recordRunner returns a canned envelope and records the exact args it received.
func recordRunner(out string, gotArgs *[]string) Runner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		*gotArgs = append([]string{name}, args...)
		return []byte(out), nil
	}
}

const okCaseEnvelope = `{"status":0,"result":{"records":[
	{"attributes":{"type":"Case"},"CaseNumber":"00012345","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
]}}`

// A record that is all digits is queried as a Case by CaseNumber, and the runner
// is invoked as `sf data query --query <soql> --json`.
func TestProbeCaseByNumber(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(okCaseEnvelope, &args)))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "00012345"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["salesforce:00012345"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	want := []string{"sf", "data", "query", "--query", "", "--json"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want shape %v", args, want)
	}
	for i, a := range want {
		if i == 4 {
			continue // the SOQL string, checked below
		}
		if args[i] != a {
			t.Errorf("args[%d] = %q, want %q", i, args[i], a)
		}
	}
	soql := args[4]
	if !strings.Contains(soql, "FROM Case") || !strings.Contains(soql, "CaseNumber IN ('00012345')") {
		t.Errorf("soql = %q, want a Case-by-CaseNumber query", soql)
	}
}

const okOppEnvelope = `{"status":0,"result":{"records":[
	{"attributes":{"type":"Opportunity"},"Id":"006XX000004Ci1wYAC","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
]}}`

// The object comes from the Lightning URL hint, not the id prefix map.
func TestProbeObjectFromURL(t *testing.T) {
	var args []string
	c := New(WithRunner(recordRunner(okOppEnvelope, &args)))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "006XX000004Ci1wYAC",
		Fields: map[string]string{"object": "Opportunity", "record": "006XX000004Ci1wYAC"},
	}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if r := out["salesforce:006XX000004Ci1wYAC"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	if !strings.Contains(args[4], "FROM Opportunity") {
		t.Errorf("soql = %q, want FROM Opportunity", args[4])
	}
}

// An id whose 3-char prefix is not in the map, with no object hint, renders
// unchecked with the no-probe reason and never reaches the runner.
func TestProbeUnknownPrefixUnchecked(t *testing.T) {
	called := false
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	c := New(WithRunner(runner))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "999XX000001AbcdEAG"}}

	out, err := c.Probe(context.Background(), ls, sources.Watermark{})
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	r := out["salesforce:999XX000001AbcdEAG"]
	if !r.Unchecked || r.Reason != probe.ReasonNoProbe {
		t.Errorf("result = %+v, want unchecked with ReasonNoProbe", r)
	}
	if called {
		t.Error("runner was called for an unmapped-prefix id; it must be filtered first")
	}
}

// A non-zero sf status whose message is an auth failure renders ReasonAuth; a
// generic failure renders ReasonError.
func TestProbeCLIFailureAuthVsError(t *testing.T) {
	authEnv := `{"status":1,"name":"NoOrgFound","message":"No authorization information found for org."}`
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(authEnv), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("auth result = %+v, want unchecked ReasonAuth", r)
	}

	errEnv := `{"status":1,"name":"InvalidQuery","message":"unexpected token near WHERE"}`
	c2 := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(errEnv), nil
	}))
	out2, _ := c2.Probe(context.Background(), ls, sources.Watermark{})
	if r := out2["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("error result = %+v, want unchecked ReasonError", r)
	}
}

// A runner that fails to produce any parseable envelope (e.g. the binary is
// missing) renders unchecked, never a false unchanged.
func TestProbeRunnerErrorUnchecked(t *testing.T) {
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, context.Canceled
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked {
		t.Errorf("result = %+v, want unchecked", r)
	}
}

// A record that fails the strict SOQL charset never reaches the runner and
// renders unchecked. This is the injection guard.
func TestProbeSOQLInjectionGuard(t *testing.T) {
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "001XX0'); DROP TABLE",
		Fields: map[string]string{"object": "Account", "record": "001XX0'); DROP TABLE"},
	}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX0'); DROP TABLE"]; !r.Unchecked {
		t.Errorf("result = %+v, want unchecked for a record that fails the charset", r)
	}
	if called {
		t.Error("runner was called with an unvalidated record; the guard must run first")
	}
}

// A malicious object name that fails the object charset also never reaches the
// runner.
func TestProbeObjectInjectionGuard(t *testing.T) {
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "001XX000003DHPhYAO",
		Fields: map[string]string{"object": "Account WHERE Id != ''; --"},
	}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("result = %+v, want unchecked ReasonError for a bad object name", r)
	}
	if called {
		t.Error("runner was called with an unvalidated object; the guard must run first")
	}
}

// A record the query did not return is unchecked, never a false unchanged.
func TestProbeMissingRecordUnchecked(t *testing.T) {
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	r := out["salesforce:001XX000003DHPhYAO"]
	if !r.Unchecked || r.LastActivity != nil {
		t.Errorf("result = %+v, want unchecked with no last activity", r)
	}
}

// A bare id with a mapped prefix and no URL hint resolves its object from the
// prefix map, here 001 -> Account.
func TestProbeObjectFromPrefix(t *testing.T) {
	acct := `{"status":0,"result":{"records":[
		{"attributes":{"type":"Account"},"Id":"001XX000003DHPhYAO","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
	]}}`
	var args []string
	c := New(WithRunner(recordRunner(acct, &args)))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time", r)
	}
	if !strings.Contains(args[4], "FROM Account") {
		t.Errorf("soql = %q, want FROM Account from the 001 prefix", args[4])
	}
}
