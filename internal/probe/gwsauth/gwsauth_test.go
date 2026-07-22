package gwsauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeRunner returns canned stdout and error for the gws export call, so tests
// never shell out to a real gws.
func fakeRunner(out string, err error) Runner {
	return func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(out), err
	}
}

// testCreds is a synthetic authorized_user blob. The secret and refresh values
// are sentinels so a leak test can assert they never reach an error string.
const testCreds = `{"client_id":"cid.apps.googleusercontent.com","client_secret":"SENTINEL_SECRET","refresh_token":"SENTINEL_REFRESH","type":"authorized_user"}`

func TestAccessTokenExchangesRefreshToken(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.mock","expires_in":3599,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	s := New(WithRunner(fakeRunner(testCreds, nil)), WithTokenURL(srv.URL))
	tok, err := s.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "ya29.mock" {
		t.Errorf("token = %q, want ya29.mock", tok)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", gotForm.Get("grant_type"))
	}
	if gotForm.Get("client_id") == "" || gotForm.Get("client_secret") != "SENTINEL_SECRET" || gotForm.Get("refresh_token") != "SENTINEL_REFRESH" {
		t.Errorf("form missing or wrong creds: %v", gotForm)
	}
}

func TestAccessTokenExportFails(t *testing.T) {
	s := New(WithRunner(fakeRunner("", errors.New("gws not logged in"))))
	if _, err := s.AccessToken(context.Background()); err == nil {
		t.Fatal("expected error when gws auth export fails")
	}
}

func TestAccessTokenIncompleteCreds(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	// refresh_token missing.
	s := New(
		WithRunner(fakeRunner(`{"client_id":"cid","client_secret":"x","type":"authorized_user"}`, nil)),
		WithTokenURL(srv.URL),
	)
	if _, err := s.AccessToken(context.Background()); err == nil {
		t.Fatal("expected error for incomplete creds")
	}
	if called {
		t.Error("token endpoint called despite incomplete creds")
	}
}

func TestAccessTokenEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	s := New(WithRunner(fakeRunner(testCreds, nil)), WithTokenURL(srv.URL))
	_, err := s.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status, got %v", err)
	}
}

func TestAccessTokenNeverLeaksSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	s := New(WithRunner(fakeRunner(testCreds, nil)), WithTokenURL(srv.URL))
	_, err := s.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	for _, secret := range []string{"SENTINEL_SECRET", "SENTINEL_REFRESH"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked secret %q: %v", secret, err)
		}
	}
}

func TestAccessTokenMalformedCredsNoLeak(t *testing.T) {
	// Malformed export JSON carrying a sentinel; the error must not echo it.
	s := New(WithRunner(fakeRunner(`{"client_secret":"SENTINEL_SECRET" oops`, nil)))
	_, err := s.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed credentials")
	}
	if strings.Contains(err.Error(), "SENTINEL_SECRET") {
		t.Errorf("error leaked malformed credential input: %v", err)
	}
}

func TestAccessTokenMalformedTokenResponseNoLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"ya29.SENTINEL_TOKEN" oops`))
	}))
	defer srv.Close()

	s := New(WithRunner(fakeRunner(testCreds, nil)), WithTokenURL(srv.URL))
	_, err := s.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed token response")
	}
	if strings.Contains(err.Error(), "SENTINEL_TOKEN") {
		t.Errorf("error leaked token fragment: %v", err)
	}
}

func TestAvailableReflectsLookPath(t *testing.T) {
	orig := lookPath
	defer func() { lookPath = orig }()

	lookPath = func(string) (string, error) { return "/usr/bin/gws", nil }
	if !Available() {
		t.Error("Available() = false when lookPath succeeds")
	}
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if Available() {
		t.Error("Available() = true when lookPath fails")
	}
}
