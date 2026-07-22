package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bashfulrobot/ballpoint/internal/probe/gwsauth"
)

// fakeExport returns a canned `gws auth export` blob so the resolution runs with
// no real gws on the host.
func fakeExport(out string, err error) gwsauth.Option {
	return gwsauth.WithRunner(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(out), err
	})
}

const fakeCreds = `{"client_id":"c","client_secret":"s","refresh_token":"r","type":"authorized_user"}`

// When gws is not on PATH the token is empty and nothing is written to stderr,
// since an absent gws is a common, expected setup rather than an error.
func TestGoogleTokenAbsentIsSilent(t *testing.T) {
	orig := gwsAvailable
	defer func() { gwsAvailable = orig }()
	gwsAvailable = func() bool { return false }

	var stderr bytes.Buffer
	if tok := googleToken(gwsauth.New(fakeExport(fakeCreds, nil)), &stderr); tok != "" {
		t.Errorf("token = %q, want empty when gws absent", tok)
	}
	if stderr.Len() != 0 {
		t.Errorf("no warning expected when gws absent, got %q", stderr.String())
	}
}

// With gws present and the exchange succeeding, the minted token is returned and
// stderr stays clean.
func TestGoogleTokenSuccess(t *testing.T) {
	orig := gwsAvailable
	defer func() { gwsAvailable = orig }()
	gwsAvailable = func() bool { return true }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"ya29.mock"}`))
	}))
	defer srv.Close()

	src := gwsauth.New(fakeExport(fakeCreds, nil), gwsauth.WithTokenURL(srv.URL))
	var stderr bytes.Buffer
	if tok := googleToken(src, &stderr); tok != "ya29.mock" {
		t.Errorf("token = %q, want ya29.mock", tok)
	}
	if stderr.Len() != 0 {
		t.Errorf("no warning expected on success, got %q", stderr.String())
	}
}

// With gws present but the acquisition failing (revoked token, offline, wedged
// CLI), the token is empty and a non-fatal warning names gws, so the failure is
// distinguishable from gws simply being absent.
func TestGoogleTokenPresentButFailingWarns(t *testing.T) {
	orig := gwsAvailable
	defer func() { gwsAvailable = orig }()
	gwsAvailable = func() bool { return true }

	src := gwsauth.New(fakeExport("", errors.New("gws not logged in")))
	var stderr bytes.Buffer
	if tok := googleToken(src, &stderr); tok != "" {
		t.Errorf("token = %q, want empty when acquisition fails", tok)
	}
	if !strings.Contains(stderr.String(), "gws") {
		t.Errorf("expected a gws warning on stderr, got %q", stderr.String())
	}
}
