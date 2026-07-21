package salesforce

import (
	"context"
	"fmt"
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

// A known-prefix id with an agreeing URL object resolves to that object and is
// queried there. The prefix (006 -> Opportunity) is authoritative; the URL hint
// names the same object, so this is the happy path where both sources agree.
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

// A malicious object name that fails the object charset never reaches the
// runner. An unknown-prefix id forces the URL object to be used, so the hostile
// name reaches the charset guard rather than being overridden by a mapped prefix.
func TestProbeObjectInjectionGuard(t *testing.T) {
	called := false
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "a0BXX0000001AbcYAM", // custom-object prefix, not in the map
		Fields: map[string]string{"object": "Account WHERE Id != ''; --"},
	}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:a0BXX0000001AbcYAM"]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("result = %+v, want unchecked ReasonError for a bad object name", r)
	}
	if called {
		t.Error("runner was called with an unvalidated object; the guard must run first")
	}
}

// A hostile Lightning URL that pairs a real id (known 006 prefix) with a
// different object name cannot redirect the query. The id prefix wins.
func TestProbeURLObjectCannotRedirectKnownPrefix(t *testing.T) {
	env := `{"status":0,"result":{"records":[
		{"attributes":{"type":"Opportunity"},"Id":"006XX000004Ci1wYAC","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
	]}}`
	var args []string
	c := New(WithRunner(recordRunner(env, &args)))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "006XX000004Ci1wYAC",
		Fields: map[string]string{"object": "User", "record": "006XX000004Ci1wYAC"},
	}}
	if _, err := c.Probe(context.Background(), ls, sources.Watermark{}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !strings.Contains(args[4], "FROM Opportunity") {
		t.Errorf("soql = %q, want FROM Opportunity from the id prefix", args[4])
	}
	if strings.Contains(args[4], "FROM User") {
		t.Errorf("soql = %q, a hostile URL object redirected the query", args[4])
	}
}

// A custom object has a prefix outside the standard map, so the URL object hint
// is the only source and is honored (the redirect fix must not break this).
func TestProbeCustomObjectFromURL(t *testing.T) {
	env := `{"status":0,"result":{"records":[
		{"attributes":{"type":"Widget__c"},"Id":"a0BXX0000001AbcYAM","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
	]}}`
	var args []string
	c := New(WithRunner(recordRunner(env, &args)))
	ls := []links.Link{{
		System: links.SystemSalesforce,
		Record: "a0BXX0000001AbcYAM",
		Fields: map[string]string{"object": "Widget__c", "record": "a0BXX0000001AbcYAM"},
	}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:a0BXX0000001AbcYAM"]; r.LastActivity == nil {
		t.Fatalf("result = %+v, want a last activity time for a custom object", r)
	}
	if !strings.Contains(args[4], "FROM Widget__c") {
		t.Errorf("soql = %q, want FROM Widget__c for a custom object", args[4])
	}
}

// More distinct sObjects than the group cap: the excess groups render unchecked
// (ReasonTooMany) instead of each spawning a subprocess.
func TestProbeCapsDistinctObjectGroups(t *testing.T) {
	calls := 0
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		calls++
		return []byte(`{"status":0,"result":{"records":[]}}`), nil
	}))
	var ls []links.Link
	for i := range maxQueryGroups + 5 {
		obj := fmt.Sprintf("Obj%02d__c", i)        // distinct custom object each
		id := fmt.Sprintf("a0BXX0000001Ab%02d", i) // 16-char, unknown prefix a0B
		ls = append(ls, links.Link{
			System: links.SystemSalesforce, Record: id,
			Fields: map[string]string{"object": obj, "record": id},
		})
	}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if calls > maxQueryGroups {
		t.Errorf("runner called %d times, want at most %d (group cap)", calls, maxQueryGroups)
	}
	tooMany := 0
	for _, r := range out {
		if r.Reason == probe.ReasonTooMany {
			tooMany++
		}
	}
	if tooMany != 5 {
		t.Errorf("ReasonTooMany count = %d, want 5 (the excess groups)", tooMany)
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

// A 15-char record id matches the record `sf` returns, even though SOQL always
// echoes the 18-char canonical Id. This is the classic-URL / old-UI-paste path.
func TestProbe15CharIDMatches18CharReturn(t *testing.T) {
	// The link carries the 15-char id; the envelope returns the 18-char form.
	env := `{"status":0,"result":{"records":[
		{"attributes":{"type":"Account"},"Id":"001XX000003DHPhYAO","LastModifiedDate":"2026-07-20T09:00:00.000+0000"}
	]}}`
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(env), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPh"}} // 15 chars

	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	r := out["salesforce:001XX000003DHPh"]
	if r.Unchecked || r.LastActivity == nil {
		t.Errorf("result = %+v, want a last activity time for a 15-char id matching the 18-char return", r)
	}
}

// A query error whose message happens to contain auth-adjacent words (session,
// login, expired) is still classified ReasonError, not ReasonAuth. The reason
// label has to stay trustworthy.
func TestProbeQueryErrorNotMisreadAsAuth(t *testing.T) {
	errEnv := `{"status":1,"name":"InvalidQuery","message":"No such column 'Session_Id__c' on entity; did the login field expire?"}`
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(errEnv), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonError {
		t.Errorf("result = %+v, want unchecked ReasonError for a query error, not ReasonAuth", r)
	}
}

// A name-only auth error (NoOrgFound with no message) is classified ReasonAuth,
// since the CLI error name has no space to match a substring like "no org".
func TestProbeNameOnlyAuthFailure(t *testing.T) {
	authEnv := `{"status":1,"name":"NoOrgFound","message":""}`
	c := New(WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(authEnv), nil
	}))
	ls := []links.Link{{System: links.SystemSalesforce, Record: "001XX000003DHPhYAO"}}
	out, _ := c.Probe(context.Background(), ls, sources.Watermark{})
	if r := out["salesforce:001XX000003DHPhYAO"]; !r.Unchecked || r.Reason != probe.ReasonAuth {
		t.Errorf("result = %+v, want unchecked ReasonAuth for a name-only NoOrgFound", r)
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
